package vtex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/store"
)

// ErrAccessKeyRequired reports that the only login method the store offers
// is an emailed access code, which cannot complete in a single call. The
// caller must run SendAccessCode, collect the code, then ValidateAccessCode.
var ErrAccessKeyRequired = errors.New("store requires an emailed access code")

type authResponse struct {
	AuthStatus string `json:"authStatus"`
	AuthCookie struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	} `json:"authCookie"`
	ExpiresIn int `json:"expiresIn"`
}

// AuthStart probes what login methods this store supports.
func (c *Client) AuthStart() (*store.Capabilities, error) {
	return store.Probe(context.Background(), c.httpClient, c.store.BaseURL, c.store.AccountName())
}

// Login authenticates with whichever strategy the store supports, preferring
// a registered OAuth driver, then classic email+password, then access key.
//
// The strategy is discovered rather than declared: a store descriptor does
// not say how to log in, the storefront does.
func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	caps, err := c.AuthStart()
	if err != nil {
		return "", err
	}

	if drv := c.store.OAuth; drv != nil && caps.HasOAuthProvider(drv.ProviderName()) {
		jwt, err := drv.Login(ctx, c.httpClient, c.store.BaseURL, email, password)
		if err != nil {
			return "", err
		}
		c.authToken = jwt
		return jwt, nil
	}

	if caps.Classic {
		return c.classicLogin(caps.AuthenticationToken, email, password)
	}

	if caps.AccessKey {
		return "", ErrAccessKeyRequired
	}

	// Name what the store does offer, so the failure tells an operator
	// exactly which driver would need writing.
	detail := "none advertised"
	if len(caps.OAuthProviders) > 0 {
		detail = "OAuth providers: " + strings.Join(caps.OAuthProviders, ", ")
	}
	return "", errfmt.Auth(fmt.Sprintf(
		"store %q offers no login method this CLI can drive (%s)", c.store.Name, detail))
}

// ClassicLogin authenticates with email and password against stock VTEX ID.
func (c *Client) ClassicLogin(email, password string) (string, error) {
	caps, err := c.AuthStart()
	if err != nil {
		return "", err
	}
	if !caps.Classic {
		return "", errfmt.Auth(fmt.Sprintf(
			"store %q has classic password login disabled", c.store.Name))
	}
	return c.classicLogin(caps.AuthenticationToken, email, password)
}

func (c *Client) classicLogin(authToken, email, password string) (string, error) {
	return c.validateAuth("/api/vtexid/pub/authentication/classic/validate", url.Values{
		"authenticationToken": {authToken},
		"login":               {email},
		"password":            {password},
	})
}

// SendAccessCode emails a one-time login code and records the pending
// authentication token, which the subsequent validate call must reuse.
func (c *Client) SendAccessCode(email string) error {
	caps, err := c.AuthStart()
	if err != nil {
		return err
	}
	if !caps.AccessKey {
		return errfmt.Auth(fmt.Sprintf(
			"store %q does not offer email access-code login", c.store.Name))
	}
	_, err = c.PostForm("/api/vtexid/pub/authentication/accesskey/send", url.Values{
		"authenticationToken": {caps.AuthenticationToken},
		"email":               {email},
	})
	if err != nil {
		return fmt.Errorf("send access code: %w", err)
	}
	return c.savePendingAuth(&PendingAuth{
		Email:               email,
		AuthenticationToken: caps.AuthenticationToken,
	})
}

// ValidateAccessCode exchanges an emailed code for a JWT. It returns the
// token and the email it authenticated, then clears the pending record.
func (c *Client) ValidateAccessCode(code, emailOverride string) (string, string, error) {
	pending, err := c.loadPendingAuth()
	if err != nil {
		return "", "", err
	}
	email := pending.Email
	if emailOverride != "" {
		email = emailOverride
	}
	jwt, err := c.validateAuth("/api/vtexid/pub/authentication/accesskey/validate", url.Values{
		"authenticationToken": {pending.AuthenticationToken},
		"login":               {email},
		"accesskey":           {code},
	})
	if err != nil {
		return "", "", err
	}
	if err := c.clearPendingAuth(); err != nil {
		return "", "", err
	}
	return jwt, email, nil
}

func (c *Client) validateAuth(path string, values url.Values) (string, error) {
	body, err := c.PostForm(path, values)
	if err != nil {
		return "", err
	}
	var resp authResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("auth validate parse: %w", err)
	}
	if resp.AuthStatus != "Success" {
		return "", errfmt.Auth("authentication failed: " + resp.AuthStatus)
	}
	// A cookie scoped to a different account would authenticate the CLI
	// against the wrong store.
	expected := c.store.AuthCookieName()
	if resp.AuthCookie.Name != expected {
		return "", errfmt.Auth(fmt.Sprintf(
			"authentication cookie mismatch: got %q, expected %q",
			resp.AuthCookie.Name, expected))
	}
	if resp.AuthCookie.Value == "" {
		return "", errfmt.Auth("authentication succeeded without returning a token")
	}
	c.authToken = resp.AuthCookie.Value
	return resp.AuthCookie.Value, nil
}

// AuthenticatedUser returns the logged-in email, or an error when the stored
// token has expired.
func (c *Client) AuthenticatedUser() (string, error) {
	body, err := c.Get("/api/vtexid/pub/authenticated/user")
	if err != nil {
		return "", err
	}
	var resp struct {
		User string `json:"user"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.User == "" {
		return "", errfmt.Auth("token expired or invalid")
	}
	return resp.User, nil
}
