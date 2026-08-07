package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/voska/vtexkit/cli/config"
	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/cli/outfmt"
	"github.com/voska/vtexkit/vtex"
)

type AuthCmd struct {
	Login  AuthLoginCmd  `cmd:"" help:"Log in with email and password, or a raw JWT."`
	Code   AuthCodeCmd   `cmd:"" help:"Log in with an emailed access code."`
	Status AuthStatusCmd `cmd:"" default:"withargs" help:"Show authentication state."`
	Logout AuthLogoutCmd `cmd:"" help:"Clear stored credentials."`
}

type AuthLoginCmd struct {
	Email    string `help:"Account email."`
	Password string `help:"Account password."`
	Token    string `help:"Paste a JWT directly instead of logging in."`
}

func (c *AuthLoginCmd) Run(g *Globals) error {
	if c.Token != "" {
		if err := g.SaveToken(c.Token); err != nil {
			return errfmt.Wrap(errfmt.ExitConfig, "store token", err)
		}
		outfmt.Success("Token stored.")
		return nil
	}

	email, password := c.Email, c.Password
	var err error
	if email == "" {
		if email, err = g.Prompt("Email: "); err != nil {
			return err
		}
	}
	if password == "" {
		if password, err = g.Prompt("Password: "); err != nil {
			return err
		}
	}

	client := vtex.New(g.Store, "")
	jwt, err := client.Login(context.Background(), email, password)
	if errors.Is(err, vtex.ErrAccessKeyRequired) {
		return errfmt.Usage(fmt.Sprintf(
			"%s uses emailed access codes — run: %s auth code send --email %s",
			g.Store.Label(), g.Store.Name, email))
	}
	if err != nil {
		return err
	}

	if err := g.SaveToken(jwt); err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "store token", err)
	}
	// The password is kept so an expired token can refresh without a
	// prompt. It lives in the OS keyring, never in a config file.
	if err := g.SavePassword(password); err != nil {
		outfmt.Warn("could not store password for auto-refresh: %v", err)
	}
	if err := g.Config().SaveCredentials(&config.Credentials{Email: email}); err != nil {
		outfmt.Warn("could not store email: %v", err)
	}

	outfmt.Success("Logged in as %s.", email)
	return nil
}

type AuthCodeCmd struct {
	Send   AuthCodeSendCmd   `cmd:"" help:"Email a one-time access code."`
	Verify AuthCodeVerifyCmd `cmd:"" help:"Exchange the emailed code for a session."`
}

type AuthCodeSendCmd struct {
	Email string `help:"Account email." required:""`
}

func (c *AuthCodeSendCmd) Run(g *Globals) error {
	if err := vtex.New(g.Store, "").SendAccessCode(c.Email); err != nil {
		return err
	}
	outfmt.Success("Code sent to %s. Run: %s auth code verify --code <code>", c.Email, g.Store.Name)
	return nil
}

type AuthCodeVerifyCmd struct {
	Code  string `help:"The code from the email." required:""`
	Email string `help:"Override the email the code was sent to."`
}

func (c *AuthCodeVerifyCmd) Run(g *Globals) error {
	client := vtex.New(g.Store, "")
	jwt, email, err := client.ValidateAccessCode(c.Code, c.Email)
	if err != nil {
		return err
	}
	if err := g.SaveToken(jwt); err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "store token", err)
	}
	if err := g.Config().SaveCredentials(&config.Credentials{Email: email}); err != nil {
		outfmt.Warn("could not store email: %v", err)
	}
	// No password is captured on this path, so tokens cannot auto-refresh.
	outfmt.Success("Logged in as %s. Access-code logins cannot auto-refresh; expect to repeat this when the token expires.", email)
	return nil
}

type AuthStatusCmd struct{}

func (c *AuthStatusCmd) Run(g *Globals) error {
	creds, _ := g.Config().LoadCredentials()
	status := map[string]any{
		"store":       g.Store.Name,
		"email":       creds.Email,
		"status":      "unauthenticated",
		"refreshable": false,
	}

	client := g.Client()
	if user, err := client.AuthenticatedUser(); err == nil {
		status["status"] = "authenticated"
		status["user"] = user
		// Auto-refresh needs a replayable password; access-code logins
		// never store one, and an agent needs to know that up front.
		if _, credErr := g.RequireAuth(); credErr == nil && creds.Email != "" {
			status["refreshable"] = true
		}
	} else if creds.Email != "" {
		status["status"] = "expired"
	}

	if g.Formatter().IsJSON() {
		return g.Formatter().Print(status)
	}
	fmt.Printf("%-12s %s\n", "store", g.Store.Label())
	fmt.Printf("%-12s %s\n", "status", status["status"])
	if creds.Email != "" {
		fmt.Printf("%-12s %s\n", "email", creds.Email)
	}
	if status["status"] != "authenticated" {
		outfmt.Hint("Run: %s auth login", g.Store.Name)
	}
	return nil
}

type AuthLogoutCmd struct{}

func (c *AuthLogoutCmd) Run(g *Globals) error {
	g.ClearSecrets()
	if err := g.Config().SaveCredentials(&config.Credentials{}); err != nil {
		return errfmt.Wrap(errfmt.ExitConfig, "clear credentials", err)
	}
	outfmt.Success("Logged out.")
	return nil
}
