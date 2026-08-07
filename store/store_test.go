package store_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/voska/vtexkit/store"
)

func TestAccountNameDerivesFromBaseURL(t *testing.T) {
	tests := []struct {
		name string
		s    store.Store
		want string
	}{
		{"explicit wins", store.Store{Account: "custom", BaseURL: "https://www.frescatto.com"}, "custom"},
		{"dot com", store.Store{BaseURL: "https://www.frescatto.com"}, "frescatto"},
		{"dot com dot br", store.Store{BaseURL: "https://www.zonasul.com.br"}, "zonasul"},
		{"no www", store.Store{BaseURL: "https://hortifruti.com.br"}, "hortifruti"},
		{"trailing path", store.Store{BaseURL: "https://www.frescatto.com/"}, "frescatto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.AccountName(); got != tt.want {
				t.Errorf("AccountName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDerivedIdentifiers(t *testing.T) {
	s := store.Store{Name: "frescatto", BaseURL: "https://www.frescatto.com"}
	if got := s.AuthCookieName(); got != "VtexIdclientAutCookie_frescatto" {
		t.Errorf("AuthCookieName() = %q", got)
	}
	if got := s.KeyringService(); got != "frescatto-cli" {
		t.Errorf("KeyringService() = %q", got)
	}
	if got := s.EnvPrefix(); got != "FRESCATTO" {
		t.Errorf("EnvPrefix() = %q", got)
	}
	// zonasul v0.5.0 ships this keyring service; migrating must not move it.
	zs := store.Store{Name: "zonasul"}
	if got := zs.KeyringService(); got != "zonasul-cli" {
		t.Errorf("zonasul KeyringService() = %q, breaks v0.5.0 compatibility", got)
	}
}

func TestQuirksHas(t *testing.T) {
	q := store.ClearSaleFingerprint | store.GatewayCallback
	if !q.Has(store.ClearSaleFingerprint) || !q.Has(store.GatewayCallback) {
		t.Error("both quirks must be set")
	}
	// The zero value is frescatto's case: stock VTEX, no deviations.
	if store.Quirks(0).Has(store.ClearSaleFingerprint) {
		t.Error("zero value must have no quirks")
	}
	if store.ClearSaleFingerprint.Has(store.GatewayCallback) {
		t.Error("quirks must be independent bits")
	}
}

func TestProbeReadsFrescattoCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vtexid/pub/authentication/start" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("scope"); got != "frescatto" {
			t.Errorf("scope = %q, want frescatto", got)
		}
		if got := r.URL.Query().Get("accountName"); got != "frescatto" {
			t.Errorf("accountName = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"authenticationToken":"TOK",
			"showClassicAuthentication":true,
			"showAccessKeyAuthentication":true,
			"oauthProviders":[]
		}`))
	}))
	defer srv.Close()

	caps, err := store.Probe(context.Background(), srv.Client(), srv.URL, "frescatto")
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Classic || !caps.AccessKey {
		t.Errorf("frescatto must report classic and accesskey, got %+v", caps)
	}
	if len(caps.OAuthProviders) != 0 {
		t.Errorf("frescatto has no oauth providers, got %v", caps.OAuthProviders)
	}
	if caps.AuthenticationToken != "TOK" {
		t.Errorf("token = %q; it must be threaded to the validate call", caps.AuthenticationToken)
	}
}

func TestProbeReadsZonaSulCustomOAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"authenticationToken":"TOK",
			"showClassicAuthentication":false,
			"showAccessKeyAuthentication":false,
			"oauthProviders":[{"providerName":"Cliente Zona Sul","className":"custom-oauth"}]
		}`))
	}))
	defer srv.Close()

	caps, err := store.Probe(context.Background(), srv.Client(), srv.URL, "zonasul")
	if err != nil {
		t.Fatal(err)
	}
	if caps.Classic || caps.AccessKey {
		t.Error("zona sul disables both stock auth paths")
	}
	if !caps.HasOAuthProvider("Cliente Zona Sul") {
		t.Errorf("providers = %v", caps.OAuthProviders)
	}
	if caps.HasOAuthProvider("Google") {
		t.Error("must not report a provider the store does not offer")
	}
}

func TestProbeSurfacesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := store.Probe(context.Background(), srv.Client(), srv.URL, "x"); err == nil {
		t.Fatal("HTTP 403 must produce an error, not empty capabilities")
	}
}
