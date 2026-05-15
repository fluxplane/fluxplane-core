package user

import "testing"

func TestNormalizeTrustEmpty(t *testing.T) {
	got := NormalizeTrust("")
	if got != TrustPublic {
		t.Fatalf("NormalizeTrust(\"\") = %q, want %q", got, TrustPublic)
	}
}

func TestNormalizeTrustPreserves(t *testing.T) {
	for _, level := range []TrustLevel{TrustPublic, TrustInternal, TrustOperator} {
		got := NormalizeTrust(level)
		if got != level {
			t.Fatalf("NormalizeTrust(%q) = %q, want same", level, got)
		}
	}
}

func TestMinReturnsMoreRestrictive(t *testing.T) {
	cases := []struct {
		a, b TrustLevel
		want TrustLevel
	}{
		{TrustOperator, TrustInternal, TrustInternal},
		{TrustInternal, TrustOperator, TrustInternal},
		{TrustPublic, TrustOperator, TrustPublic},
		{TrustOperator, TrustPublic, TrustPublic},
		{TrustInternal, TrustInternal, TrustInternal},
		{"", TrustOperator, TrustPublic}, // empty normalizes to public
	}
	for _, tc := range cases {
		got := Min(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("Min(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}
