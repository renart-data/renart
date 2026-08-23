package service

import (
	"fmt"
	"reflect"
	"time"
)

var timeType = reflect.TypeFor[time.Time]()

// cloneWorkspaceState gives the coordinator an owned, acyclic snapshot of the
// JSON-shaped workspace DTO. Keeping the clone here (rather than serializing
// through JSON) preserves concrete values stored in any fields and avoids an
// encode/decode pass on editor reads.
//
// Workspace model structs may contain slices, maps, pointers and interfaces,
// but not mutable unexported state, channels, functions, or unsafe pointers.
// New unsupported field shapes fail loudly instead of reintroducing aliasing.
func cloneWorkspaceState(state WorkspaceState) WorkspaceState {
	cloned := cloneWorkspaceValue(reflect.ValueOf(state))
	return cloned.Interface().(WorkspaceState)
}

func cloneWorkspaceValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneWorkspaceValue(value.Elem())
		result := reflect.New(value.Type()).Elem()
		result.Set(cloned)
		return result
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.New(value.Type().Elem())
		result.Elem().Set(cloneWorkspaceValue(value.Elem()))
		return result
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), value.Len(), value.Cap())
		for index := range value.Len() {
			result.Index(index).Set(cloneWorkspaceValue(value.Index(index)))
		}
		return result
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			result.SetMapIndex(iterator.Key(), cloneWorkspaceValue(iterator.Value()))
		}
		return result
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		for index := range value.Len() {
			result.Index(index).Set(cloneWorkspaceValue(value.Index(index)))
		}
		return result
	case reflect.Struct:
		if value.Type() == timeType {
			return value
		}
		result := reflect.New(value.Type()).Elem()
		for index := range value.NumField() {
			field := result.Field(index)
			if !field.CanSet() {
				panic(fmt.Sprintf("workspace snapshot contains uncloneable field %s.%s", value.Type(), value.Type().Field(index).Name))
			}
			field.Set(cloneWorkspaceValue(value.Field(index)))
		}
		return result
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		panic(fmt.Sprintf("workspace snapshot contains unsupported %s value of type %s", value.Kind(), value.Type()))
	default:
		return value
	}
}
