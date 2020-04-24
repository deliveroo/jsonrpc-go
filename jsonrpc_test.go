package jsonrpc_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deliveroo/assert-go"
	"github.com/deliveroo/jsonrpc-go"
)

func Example() {
	h := jsonrpc.New()
	h.Register(jsonrpc.Methods{
		"Hello": func(ctx context.Context, name string) (interface{}, error) {
			return jsonrpc.M{"message": fmt.Sprintf("Hello, %s", name)}, nil
		},
	})

	resp := do(h, `{
		"id": 1,
		"method": "Hello",
		"params": "Alice"
	}`)

	fmt.Println(resp.Result().Status)
	fmt.Println(resp.Body.String())

	// Output:
	//
	// 200 OK
	// {
	//   "result": {
	//     "message": "Hello, Alice"
	//   },
	//   "id": 1
	// }
}

func Example_complexParams() {
	type helloParams struct {
		Name string `json:"name"`
	}
	h := jsonrpc.New()
	h.Register(jsonrpc.Methods{
		"Hello": func(ctx context.Context, params *helloParams) (interface{}, error) {
			return jsonrpc.M{"message": fmt.Sprintf("Hello, %s", params.Name)}, nil
		},
	})

	resp := do(h, `{
		"id": 1,
		"method": "Hello",
		"params": {"name": "Alice"}
	}`)

	fmt.Println(resp.Result().Status)
	fmt.Println(resp.Body.String())

	// Output:
	//
	// 200 OK
	// {
	//   "result": {
	//     "message": "Hello, Alice"
	//   },
	//   "id": 1
	// }
}

func Example_middleware() {
	h := jsonrpc.New()
	h.Use(func(jsonrpc.Next) jsonrpc.Next {
		return func(ctx context.Context, params interface{}) (interface{}, error) {
			fmt.Printf("log: params: %q\n", params)
			return nil, nil
		}
	})
	h.Register(jsonrpc.Methods{
		"Hello": func(ctx context.Context, name string) (interface{}, error) {
			return jsonrpc.M{"message": fmt.Sprintf("Hello, %s", name)}, nil
		},
	})

	do(h, `{
		"id": 1,
		"method": "Hello",
		"params": "Alice"
	}`)

	// Output:
	//
	// log: params: "Alice"
}

func TestRPC(t *testing.T) {
	type getUserParams struct {
		Name string `json:"name"`
	}
	server := jsonrpc.New()
	server.Register(jsonrpc.Methods{
		"Echo": func(ctx context.Context, val interface{}) (interface{}, error) {
			return val, nil
		},
		"GetUser": func(ctx context.Context, params getUserParams) (interface{}, error) {
			return jsonrpc.M{"name": params.Name}, nil
		},
		"GetUser2": func(ctx context.Context, params *getUserParams) (interface{}, error) {
			return jsonrpc.M{"name": params.Name}, nil
		},
		"Now": func(ctx context.Context) (interface{}, error) {
			return time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC), nil
		},
		"Panic": func(ctx context.Context) (interface{}, error) {
			panic("panic error")
		},
		"ReturnError": func(ctx context.Context) (interface{}, error) {
			return nil, jsonrpc.NotFound("customer not found")
		},
		"ReturnErrorWithData": func(ctx context.Context) (interface{}, error) {
			return nil, jsonrpc.Error("invalid_customer", "customer failed validation").Data(jsonrpc.M{
				"name": "must be present",
			})
		},
		"Upper": func(ctx context.Context, s string) (interface{}, error) {
			return strings.ToUpper(s), nil
		},
	})

	tests := []struct {
		name   string
		req    string
		resp   string
		status int
	}{
		// Valid Requests:
		{
			name: "simple",
			req:  `{"id": 1, "method": "GetUser", "params": {"name": "Alice"}}`,
			resp: `{"id": 1, "result": {"name": "Alice"}}`,
		},
		{
			name: "simple (pointer arg)",
			req:  `{"id": 1, "method": "GetUser2", "params": {"name": "Alice"}}`,
			resp: `{"id": 1, "result": {"name": "Alice"}}`,
		},
		{
			name: "simple parameter",
			req:  `{"id": 1, "method": "Upper", "params": "hello"}`,
			resp: `{"id": 1, "result": "HELLO"}`,
		},
		{
			name: "string id",
			req:  `{"id": "a", "method": "Upper", "params": "hello"}`,
			resp: `{"id": "a", "result": "HELLO"}`,
		},
		{
			name: "batch",
			req: `[
				{"id": "a", "method": "Upper", "params": "string1"},
				{"id": "b", "method": "Upper", "params": "string2"}
			]`,
			resp: `[
				{"id": "a", "result": "STRING1"},
				{"id": "b", "result": "STRING2"}
			]`,
		},
		{
			name: "no params",
			req:  `{"id": 1, "method": "Now"}`,
			resp: `{"id": 1, "result": "2000-01-01T01:00:00Z"}`,
		},

		// RPC Errors:
		{
			name: "basic error",
			req:  `{"id": 1, "method": "ReturnError"}`,
			resp: `{"id": 1, "error": {"name": "not_found", "message": "customer not found"}}`,
		},
		{
			name: "error with data",
			req:  `{"id": 1, "method": "ReturnErrorWithData"}`,
			resp: `{
				"id": 1,
				"error": {
					"name": "invalid_customer",
					"message": "customer failed validation",
					"data": {
						"name": "must be present"
					}
				}
			}`,
		},

		// Panic Handling:
		{
			name: "panic",
			req:  `{"id": 1, "method": "Panic"}`,
			resp: `{"id": 1, "error": {"name": "internal_error", "message": "internal error"}}`,
		},

		// Invalid Requests:
		{
			name: "missing id",
			req:  `{"method": "Now"}`,
			resp: `{
				"error": {
					"name": "invalid_request",
					"message": "id must be number or string"
				},
				"id": null
			}`,
		},
		{
			name: "dupe id",
			req: `[
				{"id": 1, "method": "Now"},
				{"id": 1, "method": "Now"}
			]`,
			resp: `{
				"error": {
					"name": "invalid_request",
					"message": "ids must be unique"
				},
				"id": null
			}`,
			status: 400,
		},
		{
			name: "invalid method",
			req:  `{"id": 1, "method": "Invalid"}`,
			resp: `{
				"error": {
					"name": "method_not_found",
					"message": "method not found: Invalid"
				},
				"id": 1
			}`,
		},
		{
			name: "invalid json",
			req:  `{"id": 1`,
			resp: `{
				"error": {
					"name": "parse_error",
					"message": "cannot parse request: offset 8: unexpected end of JSON input"
				},
				"id": null
			}`,
			status: 400,
		},
		{
			name: "invalid param type",
			req:  `{"id": 1, "method": "Upper", "params": 1}`,
			resp: `{
				"error": {
					"name": "parse_error",
					"message": "cannot parse params: offset 1: cannot unmarshal number as string"
				},
				"id": 1
			}`,
		},
		{
			name: "empty batch",
			req:  `[]`,
			resp: `{
				"error": {"name": "invalid_request", "message": "empty batch"},
				"id": null
			}`,
			status: 400,
		},
	}
	for _, tt := range tests {
		if tt.status == 0 {
			tt.status = 200
		}
		t.Run(tt.name, func(t *testing.T) {
			resp := do(server, tt.req)
			assert.JSONEqual(t, resp.Body.String(), tt.resp)
			assert.Equal(t, resp.Result().StatusCode, tt.status)
		})
	}
}

func TestErrorHiding(t *testing.T) {
	server := jsonrpc.New()
	server.Register(jsonrpc.Methods{
		"Do": func(ctx context.Context) (interface{}, error) {
			return nil, errors.New("an internal error occurred")
		},
	})

	t.Run("DumpErrors=false", func(t *testing.T) {
		server.DumpErrors = false
		resp := do(server, `{"id": 1, "method": "Do"}`)
		assert.JSONEqual(t, resp.Body.String(), `{
			"error": {
				"name": "internal_error",
				"message": "internal error"
			},
			"id": 1
		}`)
	})

	t.Run("DumpErrors=true", func(t *testing.T) {
		server.DumpErrors = true
		resp := do(server, `{"id": 1, "method": "Do"}`)
		assert.JSONEqual(t, resp.Body.String(), `{
			"error": {
				"name": "internal_error",
				"message": "internal error",
				"details": [
					"an internal error occurred"
				]
			},
			"id": 1
		}`)
	})
}

func TestMiddleware(t *testing.T) {
	server := jsonrpc.New()

	var calls int
	server.Use(func(next jsonrpc.Next) jsonrpc.Next {
		return func(ctx context.Context, params interface{}) (interface{}, error) {
			calls++
			return next(ctx, params)
		}
	})
	server.Register(jsonrpc.Methods{
		"Hello": func(ctx context.Context, name string) (interface{}, error) {
			return fmt.Sprintf("Hello, %s!", name), nil
		},
	})

	resp := do(server, `{"id": 1, "method": "Hello", "params": "John"}`)
	assert.Equal(t, resp.Result().StatusCode, 200)
	assert.Equal(t, calls, 1)
}

func TestMiddlewareGroups(t *testing.T) {
	server := jsonrpc.New()

	var (
		calls   int              // overall calls
		g1      = server.Group() // group 1
		g1Calls int              // group 1 calls
		g2      = server.Group() // group 2
		g2Calls int              // group 2 calls
		noop    = jsonrpc.MethodFunc(func(context.Context) (interface{}, error) {
			return nil, nil
		})
	)

	server.Use(func(next jsonrpc.Next) jsonrpc.Next {
		return func(ctx context.Context, params interface{}) (interface{}, error) {
			calls++
			return next(ctx, params)
		}
	})
	g1.Use(func(next jsonrpc.Next) jsonrpc.Next {
		return func(ctx context.Context, params interface{}) (interface{}, error) {
			g1Calls++
			return next(ctx, params)
		}
	})
	g2.Use(func(next jsonrpc.Next) jsonrpc.Next {
		return func(ctx context.Context, params interface{}) (interface{}, error) {
			g2Calls++
			return next(ctx, params)
		}
	})
	g1.Register(jsonrpc.Methods{"G1": noop})
	g2.Register(jsonrpc.Methods{"G2": noop})

	resp := do(server, `{"id": 1, "method": "G1"}`)
	assert.Equal(t, resp.Result().StatusCode, 200)
	assert.Equal(t, calls, 1)
	assert.Equal(t, g1Calls, 1)
	assert.Equal(t, g2Calls, 0)

	resp = do(server, `{"id": 1, "method": "G2"}`)
	assert.Equal(t, resp.Result().StatusCode, 200)
	assert.Equal(t, calls, 2)
	assert.Equal(t, g1Calls, 1)
	assert.Equal(t, g2Calls, 1)
}

func TestContext(t *testing.T) {
	server := jsonrpc.New()
	var (
		gotMethod  string
		gotRequest *http.Request
	)
	server.Register(jsonrpc.Methods{
		"Do": func(ctx context.Context) (interface{}, error) {
			gotMethod = jsonrpc.MethodFromContext(ctx)
			gotRequest = jsonrpc.RequestFromContext(ctx)
			return nil, nil
		},
	})
	resp := do(server, `{"id": 1, "method": "Do"}`)
	assert.Equal(t, resp.Result().StatusCode, 200)
	assert.Equal(t, gotMethod, "Do")
	assert.NotNil(t, gotRequest)
}

func TestPreventDupeMethods(t *testing.T) {
	noop := func(context.Context) (interface{}, error) { return nil, nil }
	h := jsonrpc.New()
	h.Register(jsonrpc.Methods{
		"Do": noop,
	})

	var gotPanic interface{}
	(func() {
		defer func() { gotPanic = recover() }()
		h.Group().Register(jsonrpc.Methods{
			"Do": noop,
		})
	})()
	assert.Equal(t, gotPanic, "jsonrpc: method already registered: Do")
}

func TestPreventMiddlewareAfterRegister(t *testing.T) {
	noop := func(context.Context) (interface{}, error) { return nil, nil }
	h := jsonrpc.New()
	h.Register(jsonrpc.Methods{
		"Do": noop,
	})

	var gotPanic interface{}
	(func() {
		defer func() { gotPanic = recover() }()
		h.Use(func(next jsonrpc.Next) jsonrpc.Next {
			return func(ctx context.Context, params interface{}) (interface{}, error) {
				return next(ctx, params)
			}
		})
	})()

	assert.Equal(t, gotPanic, "jsonrpc: middleware must be registered before methods")
}

func do(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
