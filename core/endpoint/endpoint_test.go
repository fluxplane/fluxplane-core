package endpoint

import "testing"

func TestRefNewRef(t *testing.T) {
	tests := []struct {
		input    string
		expected Ref
	}{
		{"foo", "@endpoint/foo"},
		{"@endpoint/bar", "@endpoint/bar"},
		{"  baz  ", "@endpoint/baz"},
		{"  @endpoint/qux  ", "@endpoint/@endpoint/qux"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NewRef(tt.input)
			if got != tt.expected {
				t.Errorf("NewRef(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRefID(t *testing.T) {
	tests := []struct {
		ref      Ref
		expected string
	}{
		{"@endpoint/foo", "foo"},
		{"@endpoint/bar", "bar"},
		{"  @endpoint/baz  ", "@endpoint/baz"}, // ID() trims the prefix but not internal spaces
		{"@endpoint/", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.ref), func(t *testing.T) {
			got := tt.ref.ID()
			if got != tt.expected {
				t.Errorf("%q.ID() = %q, want %q", tt.ref, got, tt.expected)
			}
		})
	}
}

func TestRefValid(t *testing.T) {
	tests := []struct {
		ref      Ref
		expected bool
	}{
		{"@endpoint/foo", true},
		{"@endpoint/bar", true},
		{"", false},
		{"endpoint/foo", false},
		{"@endpoint/", false},
		{"foo", false},
		{"  @endpoint/baz  ", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.ref), func(t *testing.T) {
			got := tt.ref.Valid()
			if got != tt.expected {
				t.Errorf("%q.Valid() = %v, want %v", tt.ref, got, tt.expected)
			}
		})
	}
}

func TestSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    Spec
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid spec",
			spec:    Spec{Name: "my-endpoint", URL: "https://example.com"},
			wantErr: false,
		},
		{
			name:    "empty name",
			spec:    Spec{Name: "", URL: "https://example.com"},
			wantErr: true,
			errMsg:  "endpoint: name is empty",
		},
		{
			name:    "whitespace name",
			spec:    Spec{Name: "   ", URL: "https://example.com"},
			wantErr: true,
			errMsg:  "endpoint: name is empty",
		},
		{
			name:    "empty url",
			spec:    Spec{Name: "my-endpoint", URL: ""},
			wantErr: true,
			errMsg:  "endpoint: url is empty",
		},
		{
			name:    "whitespace url",
			spec:    Spec{Name: "my-endpoint", URL: "   "},
			wantErr: true,
			errMsg:  "endpoint: url is empty",
		},
		{
			name:    "valid with optional fields",
			spec:    Spec{Name: "ep", URL: "https://ep.example", Product: "acme", Protocol: "https", AuthRef: "auth-1", Labels: map[string]string{"env": "prod"}, Annotations: map[string]string{"note": "primary"}},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() error = nil, want %q", tt.errMsg)
				}
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
			}
		})
	}
}
