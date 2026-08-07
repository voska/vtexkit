package cli

import (
	"errors"
	"strings"
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

func TestFavFallsBackToLocalListWithoutWishlistSupport(t *testing.T) {
	// zonasul v0.5.0 shipped `fav` backed by a local list. A store with no
	// wishlist hashes must keep that behavior, not fail with a config error.
	g := testGlobals(t, store.Store{Name: "zonasul"}, &CLI{JSON: true})
	err := (&FavShowCmd{}).Run(g)
	var typed *errfmt.Error
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want a typed error", err)
	}
	if typed.Code != errfmt.ExitEmpty {
		t.Errorf("code = %d, want %d (empty) — an empty local list is emptiness, not misconfiguration",
			typed.Code, errfmt.ExitEmpty)
	}

	// And writes must land in the local list.
	if err := (&FavAddCmd{SKU: "33277"}).Run(g); err != nil {
		t.Fatalf("fav add on a local-list store: %v", err)
	}
	items, err := localFavorites(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SKU != "33277" {
		t.Errorf("local favorites = %+v, want the added SKU", items)
	}
}

func TestFavOrderRefusedForServerWishlists(t *testing.T) {
	// Bulk-ordering a curated local list is what list order does and is
	// fine; bulk-ordering a store wishlist is not.
	wl := store.Store{Name: "fr", Wishlist: store.WishlistHashes{View: "v", Add: "a", Remove: "r"}}
	g := testGlobals(t, wl, &CLI{JSON: true})
	err := (&FavOrderCmd{Qty: 1}).Run(g)
	var typed *errfmt.Error
	if !errors.As(err, &typed) || typed.Code != errfmt.ExitUsage {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "cart add") {
		t.Errorf("error must name the alternative, got: %v", err)
	}
}
