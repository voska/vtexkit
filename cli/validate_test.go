package cli

import (
	"errors"
	"testing"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/store"
)

// Agents hallucinate identifiers, and these values are interpolated straight
// into request paths.
func TestValidateIDRejectsInjection(t *testing.T) {
	bad := []struct{ name, id string }{
		{"empty", ""},
		{"query fragment", "134?foo=bar"},
		{"url fragment", "134#x"},
		{"percent encoding", "134%2F"},
		{"path traversal", "../../etc/passwd"},
		{"backslash", `134\..\x`},
		{"newline", "134\nGET /admin"},
		{"null byte", "134\x00"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateID(tt.id); err == nil {
				t.Errorf("validateID(%q) accepted a dangerous identifier", tt.id)
			}
		})
	}
}

func TestValidateIDAcceptsRealIDs(t *testing.T) {
	// Real values from both stores.
	good := []string{"134", "62", "6180", "1234-01", "v12345", "zonasulzsa"}
	for _, id := range good {
		if err := validateID(id); err != nil {
			t.Errorf("validateID(%q) = %v, want nil", id, err)
		}
	}
}

func TestFavShowOnFreshAccountIsEmptyNotMissing(t *testing.T) {
	g := testGlobals(t, store.Store{Name: "fr"}, &CLI{JSON: true})
	err := (&FavShowCmd{}).Run(g)
	var typed *errfmt.Error
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want a typed error", err)
	}
	// The user never named "favorites", so its absence is emptiness (3),
	// not a missing resource they asked for (5).
	if typed.Code != errfmt.ExitEmpty {
		t.Errorf("code = %d, want %d (empty)", typed.Code, errfmt.ExitEmpty)
	}
}
