package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Capabilities is what a live storefront reports about itself. Reading this
// is what lets a store descriptor stay three fields long.
type Capabilities struct {
	Account        string   `json:"account"`
	Classic        bool     `json:"classic"`
	AccessKey      bool     `json:"accessKey"`
	OAuthProviders []string `json:"oauthProviders"`
	// AuthenticationToken links the start call to the subsequent validate
	// call. VTEX rejects a validate that reuses a token from a different
	// start, so it must be threaded through, not re-fetched.
	AuthenticationToken string `json:"-"`
}

// HasOAuthProvider reports whether the store offers the named provider.
func (c *Capabilities) HasOAuthProvider(name string) bool {
	for _, p := range c.OAuthProviders {
		if p == name {
			return true
		}
	}
	return false
}

// Probe reads a store's auth capabilities from the public VTEX ID
// authentication-start endpoint. No credentials required, no side effects.
//
// It asks twice when it has to. Scoping the call to the account is what makes
// a store's custom OAuth provider visible, but some stores answer a scoped
// call with only that provider, hiding an access key VTEX ID still accepts.
// Prezunic reports classic=false, accessKey=false and a "Prezunic Login"
// provider when scoped, yet unscoped it reports accessKey=true — and
// accesskey/send really does email a code. Believing the scoped answer alone
// made the CLI refuse the only login that store has.
//
// Only the access key is taken from the unscoped answer. The unscoped call
// also reports classic=true for stores whose classic login is documented as
// disabled, and that claim could not be confirmed: the classic validate
// endpoint answers WrongCredentials for an unregistered address whether or
// not the method is enabled, so there is no way to tell from outside. An
// unverified capability would route a login down a path that may not work,
// so classic is left exactly as the scoped call reported it.
func Probe(ctx context.Context, c HTTPDoer, baseURL, account string) (*Capabilities, error) {
	caps, err := probeOnce(ctx, c, baseURL, account, true)
	if err != nil {
		return nil, err
	}
	if caps.Classic || caps.AccessKey {
		return caps, nil
	}
	// No generic method under the account scope. Before concluding the store
	// needs a driver, ask unscoped.
	bare, bareErr := probeOnce(ctx, c, baseURL, account, false)
	if bareErr != nil || !bare.AccessKey {
		return caps, nil
	}
	caps.AccessKey = true
	// The token must come from the same start call that advertised the
	// method: VTEX rejects a validate whose token came from a different
	// start, so taking the capability without its token would fail later.
	caps.AuthenticationToken = bare.AuthenticationToken
	return caps, nil
}

func probeOnce(ctx context.Context, c HTTPDoer, baseURL, account string, scoped bool) (*Capabilities, error) {
	endpoint := baseURL + "/api/vtexid/pub/authentication/start"
	if scoped {
		q := url.Values{
			"scope":       {account},
			"callbackUrl": {baseURL + "/api/vtexid/oauth/finish"},
			"user":        {""},
			"locale":      {"pt-BR"},
			"accountName": {account},
		}
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", baseURL, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("probe %s: HTTP %d", baseURL, resp.StatusCode)
	}

	var raw struct {
		AuthenticationToken         string `json:"authenticationToken"`
		ShowClassicAuthentication   bool   `json:"showClassicAuthentication"`
		ShowAccessKeyAuthentication bool   `json:"showAccessKeyAuthentication"`
		OAuthProviders              []struct {
			ProviderName string `json:"providerName"`
		} `json:"oauthProviders"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("probe %s: parse: %w", baseURL, err)
	}

	caps := &Capabilities{
		Account:             account,
		Classic:             raw.ShowClassicAuthentication,
		AccessKey:           raw.ShowAccessKeyAuthentication,
		AuthenticationToken: raw.AuthenticationToken,
		OAuthProviders:      make([]string, 0, len(raw.OAuthProviders)),
	}
	for _, p := range raw.OAuthProviders {
		caps.OAuthProviders = append(caps.OAuthProviders, p.ProviderName)
	}
	return caps, nil
}
