// Package store describes a VTEX storefront.
//
// The design principle is discover, don't declare: almost everything that
// differs between VTEX stores — auth capabilities, payment systems, sellers,
// delivery SLAs — is readable from the store's own public API at runtime. A
// descriptor carries only what cannot be discovered: the base URL, business
// rules with no API representation, and drivers for behavior VTEX ID cannot
// express generically.
package store

import (
	"context"
	"net/http"
	"strings"

	"github.com/voska/vtexkit/money"
)

// SearchMode selects which catalog backend to query.
type SearchMode int

const (
	// SearchAuto tries Intelligent Search REST and falls back to the
	// catalog REST API. Correct for every store observed so far.
	SearchAuto SearchMode = iota
	SearchIntelligentREST
	SearchCatalogREST
	// SearchGraphQL uses a persisted query and requires SearchHash and
	// BindingID. Only for stores that block both REST paths — the hash
	// rotates on every VTEX search-graphql release and breaks silently.
	SearchGraphQL
)

// Quirks are behavioral toggles for stores that deviate from stock VTEX.
type Quirks uint32

const (
	// ClearSaleFingerprint registers a ClearSale device fingerprint before
	// credit-card payment. Without it Zona Sul's gateway returns Cielo
	// code 59, suspected fraud.
	ClearSaleFingerprint Quirks = 1 << iota
	// GatewayCallback polls the checkout gatewayCallback endpoint after
	// payment, retrying on HTTP 428 and 500.
	GatewayCallback
)

func (q Quirks) Has(other Quirks) bool { return q&other != 0 }

// HTTPDoer is the narrow slice of *http.Client that drivers and the probe
// need. It keeps this package free of any dependency on package vtex.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// OAuthDriver drives a store-specific OAuth provider that VTEX ID cannot
// handle generically. Only stores whose classic auth is disabled need one.
type OAuthDriver interface {
	// ProviderName must match an entry in oauthProviders[].providerName
	// from the authentication-start probe.
	ProviderName() string
	// Login returns a VTEX JWT — the VtexIdclientAutCookie_<account> value.
	Login(ctx context.Context, c HTTPDoer, baseURL, email, password string) (string, error)
}

// Store describes one VTEX storefront.
type Store struct {
	// Name drives the binary name, config dir, keyring service, and env
	// var prefix. It is the one field that must never change for a
	// published CLI.
	Name        string
	DisplayName string
	BaseURL     string

	// Everything below is optional; zero values mean discover at runtime.

	// Account overrides the VTEX account name. Derived from BaseURL when
	// empty, which is correct for every store observed.
	Account string
	Search  SearchMode
	// SearchHash and BindingID apply only to SearchGraphQL.
	SearchHash string
	BindingID  string
	// Wishlist carries the persisted-query hashes for vtex.wish-list.
	// A zero value means this store's wishlist is not reachable.
	Wishlist WishlistHashes
	// MinOrder is a business rule with no API representation. Zona Sul
	// enforces R$100 but reports it only as a checkout error string.
	MinOrder money.Centavos
	OAuth    OAuthDriver
	Quirks   Quirks
}

// WishlistHashes are the persisted-query hashes for the vtex.wish-list
// operations. They are the only way to reach that API — plain,
// non-persisted queries against the same provider are rejected — and they
// are tied to the store's installed app version, so they must be captured
// per store from a live browser session.
type WishlistHashes struct {
	View   string // ViewLists
	Add    string // AddToList
	Remove string // RemoveFromList
}

// CanRead reports whether the store's wishlist can be listed.
func (w WishlistHashes) CanRead() bool { return w.View != "" }

// CanWrite reports whether the store's wishlist can be modified.
func (w WishlistHashes) CanWrite() bool { return w.Add != "" && w.Remove != "" }

// AccountName returns the VTEX account name, deriving it from the base URL
// host when not set: www.frescatto.com -> frescatto,
// www.zonasul.com.br -> zonasul.
func (s Store) AccountName() string {
	if s.Account != "" {
		return s.Account
	}
	host := s.BaseURL
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "www.")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	return host
}

// AuthCookieName is the VTEX ID cookie this store issues.
func (s Store) AuthCookieName() string {
	return "VtexIdclientAutCookie_" + s.AccountName()
}

// KeyringService namespaces secrets per store.
func (s Store) KeyringService() string { return s.Name + "-cli" }

// EnvPrefix is the uppercased store name used for env var overrides,
// e.g. FRESCATTO_JSON.
func (s Store) EnvPrefix() string { return strings.ToUpper(s.Name) }

// Label is the human-facing store name, falling back to Name.
func (s Store) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}
