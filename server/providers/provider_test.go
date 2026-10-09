package providers

import "testing"

// TestResolveSwapProportion_NilUsesConfigDefault: the recruiter's
// nullable override, when NULL, falls back to the server-wide default.
// This is the pre-feature path — every existing recruiter row has
// swap_proportion=NULL and must keep behaving as it did.
func TestResolveSwapProportion_NilUsesConfigDefault(t *testing.T) {
	got := ResolveSwapProportion(nil, 0.10)
	if got != 0.10 {
		t.Fatalf("nil override: got %v, want 0.10", got)
	}
}

// TestResolveSwapProportion_ZeroIsRespected: swap_proportion=0 is a
// LEGAL value meaning "disable swap entirely" — distinct from unset.
// Resolution must preserve 0, not reinterpret it as the default.
func TestResolveSwapProportion_ZeroIsRespected(t *testing.T) {
	zero := float32(0)
	got := ResolveSwapProportion(&zero, 0.10)
	if got != 0 {
		t.Fatalf("explicit 0 override: got %v, want 0", got)
	}
}

// TestResolveSwapProportion_PositiveOverride: a positive override wins
// over the config default.
func TestResolveSwapProportion_PositiveOverride(t *testing.T) {
	v := float32(0.25)
	got := ResolveSwapProportion(&v, 0.10)
	if got != 0.25 {
		t.Fatalf("positive override: got %v, want 0.25", got)
	}
}
