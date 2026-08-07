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

func TestFavOnStoreWithoutWishlistIsAConfigError(t *testing.T) {
	// A store with no wishlist hashes cannot reach the API at all; say so
	// rather than reporting an empty wishlist the shopper might believe.
	g := testGlobals(t, store.Store{Name: "fr"}, &CLI{JSON: true})
	err := (&FavShowCmd{}).Run(g)
	var typed *errfmt.Error
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want a typed error", err)
	}
	if typed.Code != errfmt.ExitConfig {
		t.Errorf("code = %d, want %d (config)", typed.Code, errfmt.ExitConfig)
	}
}

func TestWishlistHashCapabilities(t *testing.T) {
	none := store.WishlistHashes{}
	readOnly := store.WishlistHashes{View: "v"}
	full := store.WishlistHashes{View: "v", Add: "a", Remove: "r"}
	if none.CanRead() || none.CanWrite() {
		t.Error("zero value must have no wishlist capability")
	}
	if !readOnly.CanRead() || readOnly.CanWrite() {
		t.Error("a view hash alone is read-only")
	}
	if !full.CanRead() || !full.CanWrite() {
		t.Error("all three hashes means read and write")
	}
}
