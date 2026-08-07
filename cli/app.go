// Package cli is the complete command surface shared by every store CLI
// built on vtexkit. A store binary is a descriptor plus a call to Main.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/store"
	"golang.org/x/term"
)

// App is what a store binary supplies.
type App struct {
	Store       store.Store
	Version     string
	Description string
}

// CLI is the Kong command tree. Env var names are bound per store at parse
// time, so the same struct yields FRESCATTO_JSON and ZONASUL_JSON.
type CLI struct {
	JSON        bool             `short:"j" help:"Output JSON for agent consumption."`
	Plain       bool             `short:"p" help:"Output tab-separated values for piping."`
	Quiet       bool             `short:"q" help:"Output bare values only."`
	ResultsOnly bool             `help:"Strip the metadata envelope, output just the data."`
	Select      string           `help:"Comma-separated fields to keep, e.g. sku,name,price. Dot paths supported." placeholder:"FIELDS"`
	Fields      string           `help:"Alias for --select." hidden:""`
	NoInput     bool             `help:"Never prompt; fail instead."`
	Version     kong.VersionFlag `help:"Print version and exit."`

	Auth      AuthCmd      `cmd:"" help:"Authentication."`
	Search    SearchCmd    `cmd:"" help:"Search products."`
	Product   ProductCmd   `cmd:"" help:"Look up a product by SKU."`
	Cart      CartCmd      `cmd:"" help:"Manage the shopping cart."`
	List      ListCmd      `cmd:"" help:"Manage named SKU lists."`
	Fav       FavCmd       `cmd:"" help:"Manage favorites."`
	Delivery  DeliveryCmd  `cmd:"" help:"Delivery windows and simulation."`
	Checkout  CheckoutCmd  `cmd:"" help:"Preview or place an order."`
	Orders    OrdersCmd    `cmd:"" help:"List recent orders."`
	Store     StoreCmd     `cmd:"" help:"Store descriptor and live capabilities."`
	Doctor    DoctorCmd    `cmd:"" help:"Check whether ordering will work, and what to fix."`
	Schema    SchemaCmd    `cmd:"" help:"Dump the command tree as JSON."`
	ExitCodes ExitCodesCmd `cmd:"" name:"exit-codes" help:"Print the exit code reference."`
	Agent     AgentCmd     `cmd:"" hidden:"" help:"Deprecated alias for exit-codes."`
}

// SelectFields returns the projection list, accepting --fields as an alias
// because agents reach for both names.
func (c *CLI) SelectFields() []string {
	raw := c.Select
	if raw == "" {
		raw = c.Fields
	}
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Main parses arguments, runs the selected command, and exits with a stable
// code. It never returns.
func Main(app App) {
	var cli CLI

	// A non-TTY stdin means nothing can answer a prompt.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		cli.NoInput = true
	}

	prefix := app.Store.EnvPrefix()
	ctx := kong.Parse(&cli,
		kong.Name(app.Store.Name),
		kong.Description(app.Description),
		kong.UsageOnError(),
		kong.Vars{"version": app.Version},
		kong.DefaultEnvars(prefix),
	)

	globals := &Globals{CLI: &cli, Store: app.Store, Version: app.Version}

	if err := ctx.Run(globals); err != nil {
		exit(&cli, err)
	}
}

func exit(cli *CLI, err error) {
	var typed *errfmt.Error
	if errors.As(err, &typed) {
		report(cli, typed)
		os.Exit(typed.Code)
	}
	report(cli, &errfmt.Error{Code: errfmt.ExitError, Message: err.Error()})
	os.Exit(errfmt.ExitError)
}

// report writes the failure to stderr — structured when --json is set, so an
// agent can parse it, human-readable otherwise. Never to stdout: a caller
// piping output must not receive an error object where data was expected.
func report(cli *CLI, e *errfmt.Error) {
	if cli.JSON {
		_ = json.NewEncoder(os.Stderr).Encode(e)
		return
	}
	outfmt.ErrorMsg("%s", e.Error())
}

// Deprecated alias kept so `zonasul agent exit-codes` from v0.5.0 keeps
// working.
type AgentCmd struct {
	ExitCodes ExitCodesCmd `cmd:"" name:"exit-codes" help:"Print the exit code reference."`
}

type ExitCodesCmd struct{}

func (c *ExitCodesCmd) Run(g *Globals) error {
	return g.Formatter().Print(errfmt.ExitCodeTable())
}

type StoreCmd struct {
	Info StoreInfoCmd `cmd:"" default:"withargs" help:"Show the descriptor and probe live capabilities."`
}

type StoreInfoCmd struct{}

// Run reports what the CLI believes about the store and what the store says
// about itself. This is the first command to reach for when something breaks.
func (c *StoreInfoCmd) Run(g *Globals) error {
	info := map[string]any{
		"name":        g.Store.Name,
		"displayName": g.Store.Label(),
		"baseUrl":     g.Store.BaseURL,
		"account":     g.Store.AccountName(),
		"authCookie":  g.Store.AuthCookieName(),
		"configDir":   g.Config().Dir(),
		"keyring":     g.Store.KeyringService(),
		"envPrefix":   g.Store.EnvPrefix(),
		"minOrder":    g.Store.MinOrder,
		"quirks": map[string]bool{
			"clearSaleFingerprint": g.Store.Quirks.Has(store.ClearSaleFingerprint),
			"gatewayCallback":      g.Store.Quirks.Has(store.GatewayCallback),
		},
	}

	// Probing is best-effort: `store info` must still describe the
	// descriptor when the network is down.
	if caps, err := g.Client().AuthStart(); err == nil {
		info["capabilities"] = map[string]any{
			"classic":        caps.Classic,
			"accessKey":      caps.AccessKey,
			"oauthProviders": caps.OAuthProviders,
		}
	} else {
		info["capabilities"] = map[string]any{"error": err.Error()}
	}

	if g.Formatter().IsJSON() {
		return g.Formatter().Print(info)
	}
	fmt.Printf("%-14s %s\n", "store", g.Store.Label())
	fmt.Printf("%-14s %s\n", "url", g.Store.BaseURL)
	fmt.Printf("%-14s %s\n", "account", g.Store.AccountName())
	fmt.Printf("%-14s %s\n", "config", g.Config().Dir())
	fmt.Printf("%-14s %s\n", "keyring", g.Store.KeyringService())
	if caps, ok := info["capabilities"].(map[string]any); ok {
		if e, bad := caps["error"]; bad {
			fmt.Printf("%-14s unreachable (%v)\n", "auth", e)
		} else {
			fmt.Printf("%-14s classic=%v accessKey=%v oauth=%v\n", "auth",
				caps["classic"], caps["accessKey"], caps["oauthProviders"])
		}
	}
	return nil
}
