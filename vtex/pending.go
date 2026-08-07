package vtex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/voska/vtexkit/cli/config"
)

// PendingAuth records an in-flight access-code login. VTEX rejects a
// validate call whose authenticationToken came from a different start call,
// so the token has to survive between the two CLI invocations.
type PendingAuth struct {
	Email               string `json:"email"`
	AuthenticationToken string `json:"authenticationToken"`
}

func (c *Client) pendingAuthPath() string {
	return config.Store{Name: c.store.Name}.PendingAuthPath()
}

func (c *Client) savePendingAuth(p *PendingAuth) error {
	path := c.pendingAuthPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (c *Client) loadPendingAuth() (*PendingAuth, error) {
	data, err := os.ReadFile(c.pendingAuthPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no pending access code (run: %s auth code send)", c.store.Name)
		}
		return nil, err
	}
	var p PendingAuth
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("pending auth parse: %w", err)
	}
	return &p, nil
}

func (c *Client) clearPendingAuth() error {
	if err := os.Remove(c.pendingAuthPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pending auth: %w", err)
	}
	return nil
}
