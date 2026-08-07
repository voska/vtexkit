// Package config stores per-store CLI state under ~/.config/<store>/.
//
// Every path is derived from the store name, so two store CLIs on the same
// machine can never read or clobber each other's state. Secrets never live
// here — passwords and tokens belong in the OS keyring. The one thing this
// package holds that is close to a secret is the login email, which is kept
// so an expired token can be refreshed without prompting.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the persisted delivery address and cart pointer.
type Config struct {
	CEP          string `json:"cep,omitempty"`
	Street       string `json:"street,omitempty"`
	Number       string `json:"number,omitempty"`
	Complement   string `json:"complement,omitempty"`
	Neighborhood string `json:"neighborhood,omitempty"`
	City         string `json:"city,omitempty"`
	State        string `json:"state,omitempty"`
	OrderFormID  string `json:"orderFormId,omitempty"`
}

// Credentials holds the login email. The password lives in the OS keyring.
type Credentials struct {
	Email string `json:"email,omitempty"`
}

// Lists maps a list name to its SKUs, e.g. {"diarista": ["134", "62"]}.
type Lists map[string][]string

// Store scopes every config path to one store.
type Store struct {
	Name string
}

func (s Store) Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", s.Name)
}

func (s Store) Path() string            { return filepath.Join(s.Dir(), "config.json") }
func (s Store) CredentialsPath() string { return filepath.Join(s.Dir(), "credentials.json") }
func (s Store) ListsPath() string       { return filepath.Join(s.Dir(), "lists.json") }
func (s Store) PendingAuthPath() string { return filepath.Join(s.Dir(), "pending_auth.json") }

func (s Store) Load() (*Config, error) { return loadJSON[Config](s.Path()) }

func (s Store) Save(cfg *Config) error { return saveJSON(s.Path(), cfg) }

func (s Store) LoadCredentials() (*Credentials, error) {
	return loadJSON[Credentials](s.CredentialsPath())
}

func (s Store) SaveCredentials(c *Credentials) error {
	return saveJSON(s.CredentialsPath(), c)
}

// LoadLists returns an empty, usable map when no lists file exists, because
// callers Add to the result directly.
func (s Store) LoadLists() (Lists, error) {
	data, err := os.ReadFile(s.ListsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Lists{}, nil
		}
		return nil, err
	}
	var l Lists
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l == nil {
		return Lists{}, nil
	}
	return l, nil
}

func (s Store) SaveLists(l Lists) error { return saveJSON(s.ListsPath(), l) }

// Add appends a SKU, reporting false if it was already present.
func (l Lists) Add(name, sku string) bool {
	for _, s := range l[name] {
		if s == sku {
			return false
		}
	}
	l[name] = append(l[name], sku)
	return true
}

// Remove deletes a SKU, reporting false if it was not present.
func (l Lists) Remove(name, sku string) bool {
	skus := l[name]
	for i, s := range skus {
		if s == sku {
			l[name] = append(skus[:i], skus[i+1:]...)
			return true
		}
	}
	return false
}

// loadJSON treats a missing file as a zero value rather than an error, so a
// first run needs no setup step.
func loadJSON[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return new(T), nil
		}
		return nil, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func saveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
