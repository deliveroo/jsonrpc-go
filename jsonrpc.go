package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
)

// Handler is an http.Handler that dispatches requests to RPC handlers.
type Handler struct {
	// DumpErrors indicates if internal errors should be displayed in the
	// response; useful for local debugging.
	DumpErrors bool

	methods map[string]method
	root    *Group
}

// New returns a new initialized handler.
func New() *Handler {
	h := &Handler{
		methods: make(map[string]method),
		root:    &Group{},
	}
	h.root.server = h
	return h
}

// Group represents a set of RPC methods that share the same middleware. Groups
// may be nested, and will inherit their parent's middleware as well.
type Group struct {
	server     *Handler
	parent     *Group
	middleware []Middleware
}

// Next is the function passed into middleware to continue execution of the
// request.
type Next func(ctx context.Context, params interface{}) (interface{}, error)

// Middleware is a function that wraps an RPC method to add new behavior.
//
// For example, you might create a logging middleware that looks like:
//
//  func LoggingMiddleware(logger *logger.Logger) Middleware {
//      return func (next jsonrpc.Next) jsonrpc.Next {
//          return func(ctx context.Context, params interface{}) (interface{}, error) {
//              method := jsonrpc.MethodFromContext(ctx)
//              start := time.Now()
//              defer func() {
//                  logger.Printf("%s (%v)\n", method, time.Since(start))
//              }()
//              return next(ctx, params)
//          }
//      }
//  }
type Middleware func(Next) Next

// Methods represents a map of RPC methods.
type Methods map[string]MethodFunc

// Group creates a new subgroup, representing a group of RPC methods. This
// subgroup may have its own middleware, but will also inherit its parent's
// middleware.
func (g *Group) Group() *Group {
	return &Group{
		parent: g,
		server: g.server,
	}
}

// Group creates a new subgroup, representing a group of RPC methods. This
// subgroup may have its own middleware, but will also inherit its parent's
// middleware.
func (h *Handler) Group() *Group { return h.root.Group() }

// Use registers middleware to be used for the methods in this group.
func (g *Group) Use(middleware ...Middleware) {
	if len(g.server.methods) != 0 {
		panic("jsonrpc: middleware must be registered before methods")
	}
	g.middleware = append(g.middleware, middleware...)
}

// Use registers middleware to be used for the methods in this group.
func (h *Handler) Use(middleware ...Middleware) { h.root.Use(middleware...) }

// Register registers the set of methods owned by this group.
//
// For example:
//  g.Register(Methods{
//      "Login":   loginMethod,
//      "GetUser": getUserMethod,
//  })
func (g *Group) Register(methods Methods) {
	for name, m := range methods {
		if _, ok := g.server.methods[name]; ok {
			panic("jsonrpc: method already registered: " + name)
		}
		g.server.methods[name] = g.resolveMethod(name, m)
	}
}

// Register registers the set of methods owned by this group.
//
// For example:
//  g.Register(Methods{
//      "Login":   loginMethod,
//      "GetUser": getUserMethod,
//  })
func (h *Handler) Register(methods Methods) { h.root.Register(methods) }

type request struct {
	Method string          `json:"method"` // Method Name
	Params json.RawMessage `json:"params"` // Method Parameters
	ID     interface{}     `json:"id"`     // Request ID, useful for batches
}

type response struct {
	Result interface{} `json:"result,omitempty"`
	Error  *RPCError   `json:"error,omitempty"`
	ID     interface{} `json:"id"`
}

// M is a shorthand for map[string]interface{}. Responses from the server may be
// of this type.
type M map[string]interface{}

type contextKey int

const (
	contextKeyMethod contextKey = iota
	contextKeyRequest
)

// MethodFromContext extracts the RPC method name from the given
// context.Context.
func MethodFromContext(ctx context.Context) string {
	s, _ := ctx.Value(contextKeyMethod).(string)
	return s
}

// RequestFromContext extracts the underlying http.Request from the given
// context.Context.
func RequestFromContext(ctx context.Context) *http.Request {
	r, _ := ctx.Value(contextKeyRequest).(*http.Request)
	return r
}

// ServeHTTP implements the http.Handler interface.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithValue(r.Context(), contextKeyRequest, r)

	requests, err := parseRequests(r)
	if err != nil {
		sendJSON(w, 400, response{
			Error: translateError(err),
		})
		return
	}

	responses := make([]*response, 0, len(requests))
	for _, req := range requests {
		result, err := h.invokeMethod(ctx, req)
		responses = append(responses, &response{
			ID:     req.ID,
			Result: result,
			Error:  translateError(err),
		})
	}

	if h.DumpErrors {
		for _, r := range responses {
			if r.Error != nil {
				r.Error.dumpErrors = true
			}
		}
	}

	if len(requests) == 1 {
		sendJSON(w, 200, responses[0])
	} else {
		sendJSON(w, 200, responses)
	}
}

func (h *Handler) invokeMethod(ctx context.Context, req *request) (resp interface{}, err error) {
	// Catch panics.
	defer func() {
		if r := recover(); r != nil {
			rErr, ok := r.(error)
			if !ok {
				rErr = fmt.Errorf("%v", r)
			}
			resp = nil
			err = InternalError(rErr)
		}
	}()

	// Inject method into context.
	ctx = context.WithValue(ctx, contextKeyMethod, req.Method)

	// Validate ID.
	switch req.ID.(type) {
	case float64, string:
	default:
		return nil, InvalidRequest("id must be number or string")
	}

	// Find method.
	method, ok := h.methods[req.Method]
	if !ok {
		return nil, MethodNotFound(req.Method)
	}

	// Instantiate params, if needed.
	var params interface{}
	if method.paramsType != nil {
		// Instantiate a pointer to an empty instance of params. In other words,
		// if the method accepts `myParams`, this function will return a
		// `*myParams` pointer to an empty `myParams` instance. It must be a
		// pointer so that `json.Unmarshal` can write it.
		params = method.newParams()
		if err := json.Unmarshal(req.Params, params); err != nil {
			return nil, ParseError(err, "cannot parse params")
		}

		// Derefence the pointer from above before passing params along.
		params = reflect.ValueOf(params).Elem().Interface()
	}

	result, err := method.call(ctx, params)
	if err != nil {
		return nil, translateError(err)
	}
	return result, nil
}

func parseRequests(r *http.Request) ([]*request, error) {
	// Read body.
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, InvalidRequest("could not read body").Wrap(err)
	}
	body = bytes.TrimSpace(body)

	// Parse body.
	var result []*request
	if len(body) > 0 && body[0] == '{' {
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, ParseError(err, "cannot parse request")
		}
		result = append(result, &req)
	} else {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, ParseError(err, "cannot parse request")
		}
	}
	if len(result) == 0 {
		return nil, InvalidRequest("empty batch")
	}

	// Assert ids are unique.
	uniq := make(map[interface{}]struct{}, len(result))
	for _, req := range result {
		if _, ok := uniq[req.ID]; ok {
			return nil, InvalidRequest("ids must be unique")
		}
		uniq[req.ID] = struct{}{}
	}

	return result, nil
}

// sendJSON encodes v as JSON and writes it to the response body. Panics
// if an encoding error occurs.
func sendJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
}
