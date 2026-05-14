package resourceaddr

import "strings"

// Address is a stable, serialized address for a contributed resource.
type Address string

// String returns the serialized resource address.
func (a Address) String() string {
	return string(a)
}

// IsZero reports whether the address is empty after trimming whitespace.
func (a Address) IsZero() bool {
	return strings.TrimSpace(string(a)) == ""
}
