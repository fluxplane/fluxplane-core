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
// for values from Invocation.Input. Tags may include default=value to set a
// value when the invocation does not provide one.
func Bind[T any](inv Invocation) (T, error) {
	var zero T
	typ := reflect.TypeOf((*T)(nil)).Elem()
	value := reflect.New(typ).Elem()
	if err := bindValue(value, inv); err != nil {
		return zero, err
	}
	return value.Interface().(T), nil
}

// FlagNamesFor returns command flag names declared by command:"flag=name" tags
// on T. It is intended for presentation layers that need command completion
// metadata without duplicating command binding rules.
func FlagNamesFor[T any]() []string {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}
	out := []string{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		binding, ok, err := parseCommandBindingTag(field.Tag.Get("command"))
		if err != nil || !ok || binding.source != "flag" || binding.name == "" {
			continue
		}
		out = append(out, binding.name)
	}
	return out
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
		binding, ok, err := parseCommandBindingTag(field.Tag.Get("command"))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var values []string
		switch binding.source {
		case "":
		case "arg":
			values = append([]string(nil), inv.Args...)
		case "flag":
			if raw, ok := flagInput(inv.Input, binding.name); ok {
				values = []string{raw}
			}
		default:
			return fmt.Errorf("command: unsupported binding source %q", binding.source)
		}
		if len(values) == 0 && binding.hasDefault {
			values = []string{binding.defaultValue}
		}
		if len(values) == 0 {
			continue
		}
		if err := setFieldValue(value.Field(i), values); err != nil {
			return fmt.Errorf("command: bind %s %q: %w", binding.source, binding.name, err)
		}
	}
	return nil
}

func parseCommandTag(tag string) (source string, name string, ok bool, err error) {
	binding, ok, err := parseCommandBindingTag(tag)
	if err != nil || !ok {
		return "", "", ok, err
	}
	return binding.source, binding.name, true, nil
}

type commandBindingTag struct {
	source       string
	name         string
	defaultValue string
	hasDefault   bool
}

func parseCommandBindingTag(tag string) (commandBindingTag, bool, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" || tag == "-" {
		return commandBindingTag{}, false, nil
	}
	parts := strings.Split(tag, ",")
	binding := commandBindingTag{}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return commandBindingTag{}, false, fmt.Errorf("command: malformed tag %q", tag)
		}
		key, value, found := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !found {
			if i == 0 && part == "arg" {
				binding.source = "arg"
				continue
			}
			return commandBindingTag{}, false, fmt.Errorf("command: malformed tag %q", tag)
		}
		if key == "" || value == "" {
			return commandBindingTag{}, false, fmt.Errorf("command: malformed tag %q", tag)
		}
		if key == "default" {
			binding.defaultValue = value
			binding.hasDefault = true
			continue
		}
		if i != 0 || binding.source != "" {
			return commandBindingTag{}, false, fmt.Errorf("command: malformed tag %q", tag)
		}
		binding.source = key
		binding.name = value
	}
	if binding.source == "" && !binding.hasDefault {
		return commandBindingTag{}, false, fmt.Errorf("command: malformed tag %q", tag)
	}
	return binding, true, nil
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
