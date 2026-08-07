package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/voska/vtexkit/cli/config"
	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
	"github.com/zalando/go-keyring"
)

const (
	keyringTokenKey    = "vtex-jwt"
	keyringPasswordKey = "login-password"
)

// Globals is the context every command receives. It holds the store
// descriptor, which is what makes the whole command set store-agnostic —
// and, unlike the pre-extraction zonasul, it makes the command layer
// testable by letting a test point the store at an httptest server.
type Globals struct {
	CLI     *CLI
	Store   store.Store
	Version string
}

func (g *Globals) Config() config.Store {
	return config.Store{Name: g.Store.Name}
}

func (g *Globals) Formatter() *outfmt.Formatter {
	return outfmt.New(outfmt.Options{
		JSON:        g.CLI.JSON,
		Plain:       g.CLI.Plain,
		Quiet:       g.CLI.Quiet,
		ResultsOnly: g.CLI.ResultsOnly,
		Select:      g.CLI.SelectFields(),
	}, os.Stdout)
}

func (g *Globals) keyringService() string { return g.Store.KeyringService() }

// Client returns a client carrying whatever token is stored, without
// checking it. Commands that work unauthenticated use this.
func (g *Globals) Client() *vtex.Client {
	token, _ := keyring.Get(g.keyringService(), keyringTokenKey)
	return vtex.New(g.Store, token)
}

// RequireAuth returns an authenticated client, refreshing an expired token
// when stored credentials allow it.
func (g *Globals) RequireAuth() (*vtex.Client, error) {
	token, err := keyring.Get(g.keyringService(), keyringTokenKey)
	if err != nil || token == "" {
		return g.credentialRefresh()
	}

	client := vtex.New(g.Store, token)

	// The session API sometimes hands back a fresher cookie than the one
	// on disk; take it before spending a request on validation.
	if fresh, refreshErr := client.RefreshToken(); refreshErr == nil && fresh != "" && fresh != token {
		_ = keyring.Set(g.keyringService(), keyringTokenKey, fresh)
		return client, nil
	}

	if _, authErr := client.AuthenticatedUser(); authErr != nil {
		refreshed, credErr := g.credentialRefresh()
		if credErr != nil {
			return nil, errfmt.Auth(fmt.Sprintf("token expired (run: %s auth login)", g.Store.Name))
		}
		return refreshed, nil
	}
	return client, nil
}

// credentialRefresh re-authenticates from stored credentials. Access-key
// logins cannot be replayed, so a store using them will land here and fail
// with an actionable message rather than looping.
func (g *Globals) credentialRefresh() (*vtex.Client, error) {
	creds, err := g.Config().LoadCredentials()
	if err != nil || creds.Email == "" {
		return nil, errfmt.Auth(fmt.Sprintf("not logged in (run: %s auth login)", g.Store.Name))
	}
	password, err := keyring.Get(g.keyringService(), keyringPasswordKey)
	if err != nil || password == "" {
		return nil, errfmt.Auth(fmt.Sprintf("not logged in (run: %s auth login)", g.Store.Name))
	}

	outfmt.Hint("Token expired, re-authenticating...")
	client := vtex.New(g.Store, "")
	jwt, err := client.ClassicLogin(creds.Email, password)
	if err != nil {
		return nil, errfmt.Wrap(errfmt.ExitAuth,
			fmt.Sprintf("auto-refresh failed (run: %s auth login)", g.Store.Name), err)
	}
	_ = keyring.Set(g.keyringService(), keyringTokenKey, jwt)
	return vtex.New(g.Store, jwt), nil
}

func (g *Globals) SaveToken(jwt string) error {
	return keyring.Set(g.keyringService(), keyringTokenKey, jwt)
}

func (g *Globals) SavePassword(password string) error {
	return keyring.Set(g.keyringService(), keyringPasswordKey, password)
}

func (g *Globals) ClearSecrets() {
	_ = keyring.Delete(g.keyringService(), keyringTokenKey)
	_ = keyring.Delete(g.keyringService(), keyringPasswordKey)
}

// OrderFormID resolves the active cart, preferring the live session and
// falling back to the persisted value so a cart survives across invocations.
func (g *Globals) OrderFormID(client *vtex.Client) string {
	if sess, err := client.GetSession(); err == nil && sess.OrderFormID != "" {
		g.PersistOrderFormID(sess.OrderFormID)
		return sess.OrderFormID
	}
	if cfg, err := g.Config().Load(); err == nil && cfg.OrderFormID != "" {
		return cfg.OrderFormID
	}
	return ""
}

func (g *Globals) PersistOrderFormID(id string) {
	if id == "" {
		return
	}
	cfg, err := g.Config().Load()
	if err != nil {
		return
	}
	if cfg.OrderFormID != id {
		cfg.OrderFormID = id
		_ = g.Config().Save(cfg)
	}
}

// Prompt reads a line from stdin, refusing when input is disabled so a
// headless run fails fast instead of hanging on a closed stdin.
func (g *Globals) Prompt(label string) (string, error) {
	if g.CLI.NoInput {
		return "", errfmt.Usage(fmt.Sprintf("%s required but --no-input is set", label))
	}
	fmt.Fprint(os.Stderr, label)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", errfmt.Usage("no input received")
	}
	return strings.TrimSpace(scanner.Text()), nil
}
