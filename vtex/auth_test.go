package vtex_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// fakeOAuth stands in for a store-specific OAuth chain such as Zona Sul's.
type fakeOAuth struct {
	name   string
	jwt    string
	called bool
}

func (f *fakeOAuth) ProviderName() string { return f.name }

func (f *fakeOAuth) Login(_ context.Context, _ store.HTTPDoer, _, _, _ string) (string, error) {
	f.called = true
	return f.jwt, nil
}

// authServer routes the VTEX ID endpoints. start describes the store's
// capabilities; validate is whatever the classic/accesskey call returns.
func authServer(t *testing.T, start, validate string, seen *map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/authentication/start"):
			_, _ = w.Write([]byte(start))
		case strings.Contains(r.URL.Path, "/validate"), strings.Contains(r.URL.Path, "/send"):
			_ = r.ParseForm()
			if seen != nil {
				m := map[string]string{}
				for k := range r.Form {
					m[k] = r.Form.Get(k)
				}
				m["_contentType"] = r.Header.Get("Content-Type")
				m["_path"] = r.URL.Path
				*seen = m
			}
			_, _ = w.Write([]byte(validate))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const frescattoCaps = `{"authenticationToken":"TOK",
	"showClassicAuthentication":true,"showAccessKeyAuthentication":true,
	"oauthProviders":[]}`

const zonasulCaps = `{"authenticationToken":"TOK",
	"showClassicAuthentication":false,"showAccessKeyAuthentication":false,
	"oauthProviders":[{"providerName":"Cliente Zona Sul"}]}`

const successCookie = `{"authStatus":"Success",
	"authCookie":{"Name":"VtexIdclientAutCookie_testacct","Value":"CLASSIC-JWT"}}`

func TestLoginPrefersOAuthDriverWhenProviderMatches(t *testing.T) {
	var seen map[string]string
	srv := authServer(t, zonasulCaps, successCookie, &seen)
	drv := &fakeOAuth{name: "Cliente Zona Sul", jwt: "OAUTH-JWT"}
	c := vtex.New(store.Store{
		Name: "zs", Account: "testacct", BaseURL: srv.URL, OAuth: drv,
	}, "")

	jwt, err := c.Login(context.Background(), "a@b.c", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if jwt != "OAUTH-JWT" {
		t.Errorf("jwt = %q, want the driver's token", jwt)
	}
	if !drv.called {
		t.Error("the driver must be used when its provider is advertised")
	}
	if seen != nil {
		t.Error("classic validate must not be attempted when an OAuth driver matches")
	}
}

func TestLoginIgnoresDriverWhoseProviderIsNotAdvertised(t *testing.T) {
	srv := authServer(t, frescattoCaps, successCookie, nil)
	drv := &fakeOAuth{name: "Some Other Provider", jwt: "OAUTH-JWT"}
	c := vtex.New(store.Store{
		Name: "fr", Account: "testacct", BaseURL: srv.URL, OAuth: drv,
	}, "")

	jwt, err := c.Login(context.Background(), "a@b.c", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if drv.called {
		t.Error("a driver whose provider the store does not advertise must be skipped")
	}
	if jwt != "CLASSIC-JWT" {
		t.Errorf("jwt = %q, want the classic token", jwt)
	}
}

func TestLoginUsesClassicWithFormEncodingAndThreadedToken(t *testing.T) {
	var seen map[string]string
	srv := authServer(t, frescattoCaps, successCookie, &seen)
	c := vtex.New(store.Store{Name: "fr", Account: "testacct", BaseURL: srv.URL}, "")

	jwt, err := c.Login(context.Background(), "a@b.c", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if jwt != "CLASSIC-JWT" {
		t.Errorf("jwt = %q", jwt)
	}
	if seen["_contentType"] != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q; VTEX ID rejects JSON here", seen["_contentType"])
	}
	// VTEX rejects a validate whose token came from a different start call.
	if seen["authenticationToken"] != "TOK" {
		t.Errorf("authenticationToken = %q, want TOK", seen["authenticationToken"])
	}
	if seen["login"] != "a@b.c" || seen["password"] != "pw" {
		t.Errorf("credentials not submitted: %v", seen)
	}
	if c.AuthToken() != "CLASSIC-JWT" {
		t.Error("a successful login must update the client's token")
	}
}

func TestLoginReportsAccessKeyRequired(t *testing.T) {
	caps := `{"authenticationToken":"TOK","showClassicAuthentication":false,
		"showAccessKeyAuthentication":true,"oauthProviders":[]}`
	srv := authServer(t, caps, "", nil)
	c := vtex.New(store.Store{Name: "x", Account: "testacct", BaseURL: srv.URL}, "")

	_, err := c.Login(context.Background(), "a@b.c", "pw")
	if !errors.Is(err, vtex.ErrAccessKeyRequired) {
		t.Errorf("err = %v, want ErrAccessKeyRequired so the caller can prompt for the code", err)
	}
}

func TestLoginNamesAvailableProvidersWhenNoStrategyWorks(t *testing.T) {
	caps := `{"authenticationToken":"TOK","showClassicAuthentication":false,
		"showAccessKeyAuthentication":false,
		"oauthProviders":[{"providerName":"Google"},{"providerName":"Facebook"}]}`
	srv := authServer(t, caps, "", nil)
	c := vtex.New(store.Store{Name: "x", Account: "testacct", BaseURL: srv.URL}, "")

	_, err := c.Login(context.Background(), "a@b.c", "pw")
	if err == nil {
		t.Fatal("must fail when no strategy is available")
	}
	// The error has to say what would need implementing.
	if !strings.Contains(err.Error(), "Google") || !strings.Contains(err.Error(), "Facebook") {
		t.Errorf("error must name the available providers, got: %v", err)
	}
}

func TestValidateAuthRejectsAnotherAccountsCookie(t *testing.T) {
	wrongCookie := `{"authStatus":"Success",
		"authCookie":{"Name":"VtexIdclientAutCookie_someoneelse","Value":"X"}}`
	srv := authServer(t, frescattoCaps, wrongCookie, nil)
	c := vtex.New(store.Store{Name: "fr", Account: "testacct", BaseURL: srv.URL}, "")

	_, err := c.Login(context.Background(), "a@b.c", "pw")
	if err == nil {
		t.Fatal("a cookie scoped to a different account must be rejected")
	}
	if !strings.Contains(err.Error(), "someoneelse") {
		t.Errorf("error should name the mismatched cookie, got: %v", err)
	}
}

func TestClassicLoginReportsWrongCredentials(t *testing.T) {
	srv := authServer(t, frescattoCaps, `{"authStatus":"WrongCredentials"}`, nil)
	c := vtex.New(store.Store{Name: "fr", Account: "testacct", BaseURL: srv.URL}, "")

	_, err := c.ClassicLogin("a@b.c", "wrong")
	if err == nil || !strings.Contains(err.Error(), "WrongCredentials") {
		t.Errorf("err = %v, want the VTEX status surfaced", err)
	}
}

func TestClassicLoginRefusedWhenStoreDisablesIt(t *testing.T) {
	srv := authServer(t, zonasulCaps, "", nil)
	c := vtex.New(store.Store{Name: "zs", Account: "testacct", BaseURL: srv.URL}, "")

	_, err := c.ClassicLogin("a@b.c", "pw")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("err = %v, want a clear refusal", err)
	}
}

func TestAccessCodeRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var seen map[string]string
	srv := authServer(t, frescattoCaps, successCookie, &seen)
	c := vtex.New(store.Store{Name: "fr", Account: "testacct", BaseURL: srv.URL}, "")

	if err := c.SendAccessCode("a@b.c"); err != nil {
		t.Fatal(err)
	}

	pendingPath := filepath.Join(home, ".config", "fr", "pending_auth.json")
	info, err := os.Stat(pendingPath)
	if err != nil {
		t.Fatalf("pending auth must be persisted between invocations: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("pending_auth.json mode = %o, want 600", got)
	}

	jwt, email, err := c.ValidateAccessCode("123456", "")
	if err != nil {
		t.Fatal(err)
	}
	if jwt != "CLASSIC-JWT" || email != "a@b.c" {
		t.Errorf("jwt=%q email=%q", jwt, email)
	}
	if seen["accesskey"] != "123456" {
		t.Errorf("accesskey = %q", seen["accesskey"])
	}
	if seen["authenticationToken"] != "TOK" {
		t.Errorf("the pending token must be reused, got %q", seen["authenticationToken"])
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Error("pending auth must be cleared after a successful validate")
	}
}

func TestValidateAccessCodeHonorsEmailOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var seen map[string]string
	srv := authServer(t, frescattoCaps, successCookie, &seen)
	c := vtex.New(store.Store{Name: "fr", Account: "testacct", BaseURL: srv.URL}, "")

	if err := c.SendAccessCode("a@b.c"); err != nil {
		t.Fatal(err)
	}
	_, email, err := c.ValidateAccessCode("123456", "override@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if email != "override@example.com" || seen["login"] != "override@example.com" {
		t.Errorf("override ignored: email=%q submitted=%q", email, seen["login"])
	}
}

func TestValidateAccessCodeWithoutPendingIsActionable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := authServer(t, frescattoCaps, successCookie, nil)
	c := vtex.New(store.Store{Name: "fr", Account: "testacct", BaseURL: srv.URL}, "")

	_, _, err := c.ValidateAccessCode("123456", "")
	if err == nil {
		t.Fatal("must fail with no pending code")
	}
	// The error has to tell the caller the exact command to run next.
	if !strings.Contains(err.Error(), "fr auth code send") {
		t.Errorf("error must name the recovery command, got: %v", err)
	}
}

func TestAuthenticatedUserDetectsExpiredToken(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user":""}`))
	})
	_, err := c.AuthenticatedUser()
	if err == nil {
		t.Fatal("an empty user means the token is dead")
	}
}
