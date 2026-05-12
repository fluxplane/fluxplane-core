package tool

import "testing"

func TestSpecValidateRejectsEmptyName(t *testing.T) {
	err := Spec{}.Validate()
	if err == nil {
		t.Fatal("Validate error is nil, want empty name error")
	}
}
