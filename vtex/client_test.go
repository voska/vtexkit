package vtex_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// newTestClient wires a client to a throwaway server. Every test in this
// package uses it, so the store descriptor stays consistent: account
// "testacct" means the auth cookie is VtexIdclientAutCookie_testacct.
func newTestClient(t *testing.T, h http.HandlerFunc) (*vtex.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := store.Store{Name: "test", Account: "testacct", BaseURL: srv.URL}
	return vtex.New(s, "TOKEN"), srv
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAuthCookieUsesStoreAccount(t *testing.T) {
	var gotCookie string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie("VtexIdclientAutCookie_testacct"); err == nil {
			gotCookie = ck.Value
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.Get("/anything"); err != nil {
		t.Fatal(err)
	}
	if gotCookie != "TOKEN" {
		t.Errorf("cookie = %q, want TOKEN — the cookie name must come from the descriptor", gotCookie)
	}
}

func TestNoCookieSentWhenUnauthenticated(t *testing.T) {
	var sawCookie bool
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("VtexIdclientAutCookie_testacct"); err == nil {
			sawCookie = true
		}
		_, _ = w.Write([]byte(`{}`))
	})
	c.SetAuthToken("")
	if _, err := c.Get("/anything"); err != nil {
		t.Fatal(err)
	}
	if sawCookie {
		t.Error("an empty token must not produce a cookie header")
	}
}

func TestHTTPStatusMapsToExitCodes(t *testing.T) {
	tests := []struct {
		status   int
		wantCode int
	}{
		{http.StatusUnauthorized, errfmt.ExitAuth},
		{http.StatusForbidden, errfmt.ExitForbidden},
		{http.StatusNotFound, errfmt.ExitNotFound},
		{http.StatusTooManyRequests, errfmt.ExitRateLimit},
		{http.StatusServiceUnavailable, errfmt.ExitRetryable},
	}
	for _, tt := range tests {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
		})
		_, err := c.Get("/x")
		var e *errfmt.Error
		if !errors.As(err, &e) {
			t.Errorf("HTTP %d produced %v, want a typed *errfmt.Error", tt.status, err)
			continue
		}
		if e.Code != tt.wantCode {
			t.Errorf("HTTP %d mapped to exit %d, want %d", tt.status, e.Code, tt.wantCode)
		}
	}
}

func TestPostJSONSetsContentType(t *testing.T) {
	var gotCT, gotMethod string
	var gotBody map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.PostJSON("/x", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotCT != "application/json" {
		t.Errorf("method=%s content-type=%s", gotMethod, gotCT)
	}
	if gotBody["a"] != "b" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestPostFormUsesFormEncoding(t *testing.T) {
	var gotCT, gotLogin string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotLogin = r.Form.Get("login")
		_, _ = w.Write([]byte(`{}`))
	})
	// VTEX ID rejects JSON on its validate endpoints.
	if _, err := c.PostForm("/x", url.Values{"login": {"a@b.c"}}); err != nil {
		t.Fatal(err)
	}
	if gotCT != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotLogin != "a@b.c" {
		t.Errorf("login = %q", gotLogin)
	}
}

func TestPatchJSON(t *testing.T) {
	var gotMethod string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{}`))
	})
	if _, err := c.PatchJSON("/x", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
}

func TestPostJSONAbsoluteSetsOriginFromStore(t *testing.T) {
	var gotOrigin, gotReferer string
	var srvURL string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		_, _ = w.Write([]byte(`{}`))
	})
	srvURL = srv.URL
	if _, err := c.PostJSONAbsolute(srv.URL+"/gateway", []any{}); err != nil {
		t.Fatal(err)
	}
	if gotOrigin != srvURL {
		t.Errorf("Origin = %q, want the store base URL %q", gotOrigin, srvURL)
	}
	if gotReferer != srvURL+"/" {
		t.Errorf("Referer = %q", gotReferer)
	}
}

func TestGetSessionLooksUpAccountScopedCookie(t *testing.T) {
	var gotItems string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotItems = r.URL.Query().Get("items")
		// The cookie key is account-scoped; a fixed struct tag cannot read it.
		_, _ = w.Write([]byte(`{"namespaces":{
			"cookie":{"VtexIdclientAutCookie_testacct":{"value":"JWT"}},
			"checkout":{"orderFormId":{"value":"OF1"}},
			"authentication":{"storeUserEmail":{"value":"a@b.c"}}}}`))
	})
	sess, err := c.GetSession()
	if err != nil {
		t.Fatal(err)
	}
	if sess.AuthToken != "JWT" {
		t.Errorf("AuthToken = %q, want JWT", sess.AuthToken)
	}
	if sess.OrderFormID != "OF1" || sess.Email != "a@b.c" {
		t.Errorf("session = %+v", sess)
	}
	if gotItems == "" {
		t.Error("items query param is required by the sessions API")
	}
}

func TestGetSessionIgnoresAnotherAccountsCookie(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// A cookie for a different store must never be picked up.
		_, _ = w.Write([]byte(`{"namespaces":{
			"cookie":{"VtexIdclientAutCookie_someoneelse":{"value":"WRONG"}},
			"checkout":{"orderFormId":{"value":"OF1"}}}}`))
	})
	sess, err := c.GetSession()
	if err != nil {
		t.Fatal(err)
	}
	if sess.AuthToken != "" {
		t.Errorf("AuthToken = %q, want empty — that cookie belongs to another account", sess.AuthToken)
	}
}

func TestRefreshTokenReturnsEmptyWhenSessionHasNone(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"namespaces":{"cookie":{},"checkout":{}}}`))
	})
	got, err := c.RefreshToken()
	if err != nil {
		t.Fatalf("an expired session is not an error, it means re-authenticate: %v", err)
	}
	if got != "" {
		t.Errorf("token = %q, want empty", got)
	}
}

func TestRefreshTokenUpdatesClientToken(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"namespaces":{
			"cookie":{"VtexIdclientAutCookie_testacct":{"value":"FRESH"}},
			"checkout":{"orderFormId":{"value":""}}}}`))
	})
	got, err := c.RefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "FRESH" {
		t.Errorf("returned %q, want FRESH", got)
	}
	if c.AuthToken() != "FRESH" {
		t.Errorf("client token = %q; refresh must update it in place", c.AuthToken())
	}
}
