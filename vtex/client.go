// Package vtex is a client for the public VTEX storefront APIs: catalog
// search, checkout orderForm, delivery simulation, VTEX ID auth, and order
// history.
//
// Nothing here is store-specific. Every store-dependent value comes from the
// store.Store descriptor the client is constructed with.
package vtex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/store"
)

// defaultTimeout bounds every request. The previous implementations used a
// zero-value http.Client, so a hung storefront blocked a CLI invocation
// forever with no way out but Ctrl-C.
const defaultTimeout = 30 * time.Second

type Client struct {
	store      store.Store
	authToken  string
	httpClient *http.Client

	// GatewayURL overrides the payment gateway host. Tests set it; empty
	// means the production vtexpayments host derived from the account.
	GatewayURL string
	// ClearSaleURL overrides the ClearSale fingerprint host for tests.
	ClearSaleURL string
	// SettlementInterval overrides the delay between order settlement
	// polls. Tests shorten it; zero means the default.
	SettlementInterval time.Duration
}

func New(s store.Store, authToken string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		store:      s,
		authToken:  authToken,
		httpClient: &http.Client{Jar: jar, Timeout: defaultTimeout},
	}
}

func (c *Client) Store() store.Store        { return c.store }
func (c *Client) HTTPClient() *http.Client  { return c.httpClient }
func (c *Client) SetAuthToken(token string) { c.authToken = token }
func (c *Client) AuthToken() string         { return c.authToken }

func (c *Client) do(req *http.Request) ([]byte, error) {
	if c.authToken != "" {
		req.AddCookie(&http.Cookie{
			Name:  c.store.AuthCookieName(),
			Value: c.authToken,
		})
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return body, httpError(resp.StatusCode, string(body))
	}
	return body, nil
}

// httpError maps transport status to the CLI's stable exit codes so an agent
// can tell a retryable failure from a permanent one without parsing text.
func httpError(statusCode int, body string) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return errfmt.Auth(fmt.Sprintf("HTTP 401: %s", body))
	case http.StatusForbidden:
		return errfmt.Forbidden(fmt.Sprintf("HTTP 403: %s", body))
	case http.StatusNotFound:
		return errfmt.NotFound(fmt.Sprintf("HTTP 404: %s", body))
	case http.StatusTooManyRequests:
		return errfmt.RateLimit()
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return errfmt.Retryable(fmt.Sprintf("HTTP %d: upstream unavailable", statusCode))
	default:
		return fmt.Errorf("HTTP %d: %s", statusCode, body)
	}
}

func (c *Client) Get(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.store.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) PostJSON(path string, payload any) ([]byte, error) {
	return c.sendJSON(http.MethodPost, c.store.BaseURL+path, payload)
}

func (c *Client) PatchJSON(path string, payload any) ([]byte, error) {
	return c.sendJSON(http.MethodPatch, c.store.BaseURL+path, payload)
}

func (c *Client) sendJSON(method, fullURL string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, fullURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// PostForm submits form-encoded values. VTEX ID's validate endpoints reject
// JSON bodies, so auth uses this rather than PostJSON.
func (c *Client) PostForm(path string, values url.Values) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, c.store.BaseURL+path,
		bytes.NewBufferString(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// PostJSONAbsolute posts to a full URL rather than a storefront path. The
// payment gateway lives on a different host, so it cannot use PostJSON.
func (c *Client) PostJSONAbsolute(absoluteURL string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, absoluteURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", c.store.BaseURL)
	req.Header.Set("Referer", c.store.BaseURL+"/")
	return c.do(req)
}
