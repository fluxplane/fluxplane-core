package command

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Bind binds a command invocation into a struct using command tags.
// Supported tags are command:"arg" for positional Args and command:"flag=name"
// for values from Invocation.Input.
func Bind[T any](inv Invocation) (T, error) {
	var zero T
	typ := reflect.TypeOf((*T)(nil)).Elem()
	value := reflect.New(typ).Elem()
	if err := bindValue(value, inv); err != nil {
		return zero, err
	}
	return value.Interface().(T), nil
}

func bindValue(value reflect.Value, inv Invocation) error {
	typ := value.Type()
	if typ.Kind() == reflect.Pointer {
		if typ.Elem().Kind() != reflect.Struct {
			return fmt.Errorf("command: typed input %s must point to a struct", typ)
		}
		value.Set(reflect.New(typ.Elem()))
		return bindStruct(value.Elem(), inv)
	}
	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("command: typed input %s must be a struct", typ)
	}
	return bindStruct(value, inv)
}

func bindStruct(value reflect.Value, inv Invocation) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		source, name, ok, err := parseCommandTag(field.Tag.Get("command"))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var values []string
		switch source {
		case "arg":
			values = append([]string(nil), inv.Args...)
		case "flag":
			if raw, ok := flagInput(inv.Input, name); ok {
				values = []string{raw}
			}
		default:
			return fmt.Errorf("command: unsupported binding source %q", source)
		}
		if len(values) == 0 {
			continue
		}
		if err := setFieldValue(value.Field(i), values); err != nil {
			return fmt.Errorf("command: bind %s %q: %w", source, name, err)
		}
	}
	return nil
}

func parseCommandTag(tag string) (source string, name string, ok bool, err error) {
	tag = strings.TrimSpace(tag)
	if tag == "" || tag == "-" {
		return "", "", false, nil
	}
	key, value, found := strings.Cut(tag, "=")
	if !found {
		if tag == "arg" {
			return "arg", "", true, nil
		}
		return "", "", false, fmt.Errorf("command: malformed tag %q", tag)
	}
	if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return "", "", false, fmt.Errorf("command: malformed tag %q", tag)
	}
	return strings.TrimSpace(key), strings.TrimSpace(value), true, nil
}

func flagInput(input any, name string) (string, bool) {
	values, ok := input.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := values[name]
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return fmt.Sprint(typed), true
	}
}

func setFieldValue(field reflect.Value, values []string) error {
	if !field.CanSet() {
		return nil
	}
	if field.Kind() == reflect.Pointer {
		ptr := reflect.New(field.Type().Elem())
		if err := setFieldValue(ptr.Elem(), values); err != nil {
			return err
		}
		field.Set(ptr)
		return nil
	}
	if field.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(field.Type(), 0, len(values))
		for _, raw := range values {
			elem := reflect.New(field.Type().Elem()).Elem()
			if err := setScalarValue(elem, raw); err != nil {
				return err
			}
			slice = reflect.Append(slice, elem)
		}
		field.Set(slice)
		return nil
	}
	return setScalarValue(field, strings.Join(values, " "))
}

func setScalarValue(field reflect.Value, value string) error {
	if field.CanAddr() {
		if unmarshaler, ok := field.Addr().Interface().(encoding.TextUnmarshaler); ok {
			return unmarshaler.UnmarshalText([]byte(value))
		}
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
		return nil
	case reflect.Bool:
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(parsed)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
		return nil
	default:
		return fmt.Errorf("unsupported field type %s", field.Type())
	}
}
