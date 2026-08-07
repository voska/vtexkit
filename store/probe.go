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
func Probe(ctx context.Context, c HTTPDoer, baseURL, account string) (*Capabilities, error) {
	q := url.Values{
		"scope":       {account},
		"callbackUrl": {baseURL + "/api/vtexid/oauth/finish"},
		"user":        {""},
		"locale":      {"pt-BR"},
		"accountName": {account},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/api/vtexid/pub/authentication/start?"+q.Encode(), nil)
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
