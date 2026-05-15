package resourceaddr

import "testing"

func TestAddressString(t *testing.T) {
	a := Address("some/path:resource")
	if got := a.String(); got != "some/path:resource" {
		t.Fatalf("String() = %q, want %q", got, "some/path:resource")
	}
}

func TestAddressIsZeroEmpty(t *testing.T) {
	if !Address("").IsZero() {
		t.Fatal("IsZero(\"\") = false, want true")
	}
	if !Address("   ").IsZero() {
		t.Fatal("IsZero(\"   \") = false, want true")
	}
}

func TestAddressIsZeroNonEmpty(t *testing.T) {
	if Address("x").IsZero() {
		t.Fatal("IsZero(\"x\") = true, want false")
	}
}
