package jsonrpc

import (
	"context"
	"fmt"
	"reflect"
)

type MethodFunc interface{}

type method struct {
	Name       string
	fn         reflect.Value
	paramsType reflect.Type

	call func(context.Context, interface{}) (interface{}, error)
}

var (
	typeContextContext = reflect.TypeOf((*context.Context)(nil)).Elem()
	typeEmptyInterface = reflect.TypeOf((*interface{})(nil)).Elem()
	typeError          = reflect.TypeOf((*error)(nil)).Elem()
)

func (g *Group) resolveMethod(name string, fn MethodFunc) method {
	val := reflect.ValueOf(fn)
	if val.Kind() != reflect.Func {
		panic(val.Type().String() + " is not a function")
	}

	// Validate signature.
	t := val.Type()
	valid := (t.NumIn() == 1 || t.NumIn() == 2) &&
		t.In(0) == typeContextContext &&
		t.NumOut() == 2 &&
		t.Out(0) == typeEmptyInterface &&
		t.Out(1) == typeError
	if !valid {
		panic(fmt.Sprintf("invalid signature: "+
			"want func(ctx context.Context, params T) (interface{}, error) or "+
			"func(ctx context.Context) (interface{}, error), "+
			"got %v", val.Type()))
	}
	m := method{
		Name: name,
		fn:   val,
	}
	if t.NumIn() == 2 {
		m.paramsType = t.In(1)
	}

	m.call = func(ctx context.Context, params interface{}) (interface{}, error) {
		args := append(make([]reflect.Value, 0, 2),
			reflect.ValueOf(ctx),
		)
		if m.paramsType != nil {
			args = append(args,
				reflect.ValueOf(params),
			)
		}
		outs := m.fn.Call(args)
		result, errVal := outs[0].Interface(), outs[1].Interface()
		err, _ := errVal.(error)
		return result, err
	}

	// Apply middleware.
	cnt := 0
	for {
		for i := len(g.middleware) - 1; i >= 0; i-- {
			m.call = g.middleware[i](m.call)
			cnt++
		}
		if g.parent == nil {
			break
		}
		g = g.parent
	}

	return m
}

// newParams allocates a new instance of the params expected by this RPC Method.
func (m *method) newParams() interface{} {
	t := m.paramsType
	result := reflect.New(t)
	for t.Kind() == reflect.Ptr {
		result.Elem().Set(reflect.Zero(t))
		t = t.Elem()
	}
	return result.Interface()
}
