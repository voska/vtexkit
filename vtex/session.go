package vtex

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Session is the storefront's view of the current visitor.
type Session struct {
	AuthToken   string `json:"authToken"`
	OrderFormID string `json:"orderFormId"`
	Email       string `json:"email"`
}

// GetSession reads the auth cookie, cart pointer, and logged-in email from
// the VTEX sessions API.
//
// The auth cookie key is account-scoped (VtexIdclientAutCookie_frescatto),
// so the response cannot be unmarshalled through a fixed struct tag the way
// the pre-extraction implementations did. It is decoded into a map and
// looked up by the descriptor's cookie name.
func (c *Client) GetSession() (*Session, error) {
	cookieName := c.store.AuthCookieName()
	items := strings.Join([]string{
		"cookie." + cookieName,
		"checkout.orderFormId",
		"authentication.storeUserEmail",
	}, ",")

	body, err := c.Get("/api/sessions?items=" + url.QueryEscape(items))
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var resp struct {
		Namespaces struct {
			Cookie map[string]struct {
				Value string `json:"value"`
			} `json:"cookie"`
			Checkout struct {
				OrderFormID struct {
					Value string `json:"value"`
				} `json:"orderFormId"`
			} `json:"checkout"`
			Authentication struct {
				Email struct {
					Value string `json:"value"`
				} `json:"storeUserEmail"`
			} `json:"authentication"`
		} `json:"namespaces"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}

	return &Session{
		AuthToken:   resp.Namespaces.Cookie[cookieName].Value,
		OrderFormID: resp.Namespaces.Checkout.OrderFormID.Value,
		Email:       resp.Namespaces.Authentication.Email.Value,
	}, nil
}

// RefreshToken asks the sessions API for a fresher JWT. It returns an empty
// string when the session carries no token, which means the caller must
// re-authenticate rather than treat it as an error.
func (c *Client) RefreshToken() (string, error) {
	sess, err := c.GetSession()
	if err != nil {
		return "", err
	}
	if sess.AuthToken == "" {
		return "", nil
	}
	c.authToken = sess.AuthToken
	return sess.AuthToken, nil
}
