package operationruntime

import (
	"fmt"

	"github.com/fluxplane/fluxplane-core/core/operation"
)

// Validator validates operation input/output values against declared types.
type Validator interface {
	Validate(typ operation.Type, value operation.Value) error
}

// ValidatorFunc adapts a function into a Validator.
type ValidatorFunc func(operation.Type, operation.Value) error

// Validate validates value against typ.
func (f ValidatorFunc) Validate(typ operation.Type, value operation.Value) error {
	if f == nil {
		return nil
	}
	return f(typ, value)
}

func validationFailed(kind string, err error) operation.Result {
	if err == nil {
		return operation.OK(nil)
	}
	return operation.Failed("validation_failed", fmt.Sprintf("%s validation failed: %v", kind, err), map[string]any{
		"kind": kind,
	})
}
