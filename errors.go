package jsonrpc

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ErrorOption func(*RPCError)

func Wrap(wrapped error) ErrorOption {
	return func(err *RPCError) { err.wrapped = wrapped }
}

func Data(data interface{}) ErrorOption {
	return func(err *RPCError) { err.data = data }
}

func Error2(name, message string, args ...interface{}) *RPCError {
	result := &RPCError{Name: name, Message: message}
	for i := len(args) - 1; i >= 0; i-- {
		if opt, ok := args[i].(ErrorOption); ok {
			opt(result)
			args = args[0:i]
		}
	}

	if len(args) > 0 {
		result.Message = fmt.Sprintf(message, args...)
	}
	return result
}

// Error creates an error that will be rendered directly to the client.
func Error(name, message string, args ...interface{}) *RPCError {
	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	return &RPCError{
		Name:    name,
		Message: message,
	}
}

// InternalError indicates a fault internal to the server, having nothing to do
// with the client request. This error corresponds to HTTP status code 500.
//
// Note that unless DumpErrors is specified, data about the error is
// deliberately omitted to avoid transmitting sensitive information to the
// client.
func InternalError(err error) *RPCError {
	return Error("internal_error", "internal error").Wrap(err)
}

// InvalidParams indicates the client sent invalid method parameters.
func InvalidParams(msg string, args ...interface{}) *RPCError {
	return Error("invalid_params", msg, args...)
}

// InvalidRequest indicates the client sent a malformed request.
func InvalidRequest(msg string, args ...interface{}) *RPCError {
	return Error("invalid_request", msg, args...)
}

// MethodNotFound indicates the client called a non-existent method.
func MethodNotFound(method string) *RPCError {
	return Error("method_not_found", "method not found: %s", method)
}

// NotFound indicates that a requested entity could not be found.
func NotFound(msg string, args ...interface{}) *RPCError {
	return Error("not_found", msg, args...)
}

// ParseError indicates that invalid JSON was received by the server. The error
// provided will be used to provide a sanitized message to the caller describing
// the JSON parse error.
func ParseError(err error, msg string) *RPCError {
	if details := jsonErrorDetails(err); details != "" {
		msg += ": " + details
	}
	return Error("parse_error", msg).Wrap(err)
}

// Unauthorized indicates the client must be authenticated.
func Unauthorized(msg string, args ...interface{}) *RPCError {
	return Error("unauthorized", msg, args...)
}

// RPCError is an error that will be returned to the client. If it wraps an
// underlying error, and DumpErrors is enabled on the server, the underlying
// error will be returned under "details" as an array of strings (split on
// newline).
//
// Example:
//	{
//		"name": "method_not_found",
//		"message": "method not found: InvalidMethod"
//	}
//
type RPCError struct {
	// Name is the machine-parseable name of the error. Error names should be in
	// snake_case (e.g. "invalid_account").
	Name string

	// Message is the human-readable message of the error.
	Message string

	data       interface{} // optional additional error info
	dumpErrors bool        // should wrapped error be rendered?
	wrapped    error       // optional underlying error
}

// Data sets additional information about the error. This may be a primitive or
// a structured object.
func (e *RPCError) Data(data interface{}) *RPCError {
	e.data = data
	return e
}

// Wrap sets the underlying error that caused this RPC error.
func (e *RPCError) Wrap(err error) *RPCError {
	e.wrapped = err
	return e
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	s := "jsonrpc: " + strings.ReplaceAll(e.Name, "_", " ")
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.wrapped != nil {
		s += ": " + e.wrapped.Error()
	}
	return s
}

// Unwrap returns the wrapped error, if any.
func (e *RPCError) Unwrap() error {
	return e.wrapped
}

// MarshalJSON implements the json.Marshaler interface.
func (e *RPCError) MarshalJSON() ([]byte, error) {
	var result struct {
		Name    string      `json:"name"`
		Message string      `json:"message"`
		Data    interface{} `json:"data,omitempty"`
		Details []string    `json:"details,omitempty"`
	}
	result.Name = e.Name
	result.Message = e.Message
	result.Data = e.data
	if e.dumpErrors && e.wrapped != nil {
		s := fmt.Sprintf("%+v", e.wrapped)      // stringify
		s = strings.Replace(s, "\t", "  ", -1)  // tabs to spaces
		result.Details = strings.Split(s, "\n") // split on newline
	}
	return json.Marshal(result)
}

// translateError coerces err into an RPCError that can be marshaled directly
// to the client.
func translateError(err error) *RPCError {
	if err == nil {
		return nil
	}
	switch err := err.(type) {
	case *RPCError:
		return err
	default:
		return InternalError(err)
	}
}
