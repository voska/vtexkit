package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/vtex"
)

type DeliveryCmd struct {
	Windows  DeliveryWindowsCmd  `cmd:"" default:"1" help:"List delivery windows for the current cart."`
	Simulate DeliverySimulateCmd `cmd:"" help:"Check delivery for a CEP without logging in."`
}

type DeliveryWindowsCmd struct {
	Limit int `help:"Maximum windows to show. 0 shows all." default:"0"`
}

func (c *DeliveryWindowsCmd) Run(g *Globals) error {
	client, err := g.RequireAuth()
	if err != nil {
		return err
	}
	windows, err := client.GetDeliveryWindows(g.OrderFormID(client))
	if err != nil {
		return err
	}
	if len(windows) == 0 {
		return errfmt.Domain(
			"no delivery windows — the cart may be empty or the address unserviceable")
	}
	return printWindows(g, windows, c.Limit)
}

func printWindows(g *Globals, windows []vtex.DeliveryWindow, limit int) error {
	if limit > 0 && limit < len(windows) {
		windows = windows[:limit]
	}
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(windows)
	}
	for _, w := range windows {
		price := "free"
		if w.Price > 0 {
			price = w.Price.String()
		}
		fmt.Printf("%-4d %s  %s–%s  %s\n",
			w.Index,
			w.Start.Local().Format("Mon 02 Jan"),
			w.Start.Local().Format("15:04"),
			w.End.Local().Format("15:04"),
			price)
	}
	return nil
}

// DeliverySimulateCmd checks delivery without authentication, which is how
// windows and payment methods can be inspected before anyone logs in.
type DeliverySimulateCmd struct {
	CEP     string `help:"Postal code. Falls back to the configured address."`
	SKU     string `help:"SKU to simulate with. Required unless the cart has items."`
	Qty     int    `help:"Quantity." default:"1"`
	Seller  string `help:"Seller id. Discovered from the catalog when omitted."`
	Windows int    `help:"Maximum windows to show." default:"10"`
}

func (c *DeliverySimulateCmd) Run(g *Globals) error {
	cep := c.CEP
	if cep == "" {
		cfg, err := g.Config().Load()
		if err == nil {
			cep = cfg.CEP
		}
	}
	if cep == "" {
		return errfmt.Usage("--cep required (no address configured)")
	}
	if c.SKU == "" {
		return errfmt.Usage("--sku required")
	}
	if err := validateID(c.SKU); err != nil {
		return err
	}

	client := g.Client()
	seller := c.Seller
	if seller == "" {
		var err error
		if seller, err = discoverSeller(client, c.SKU); err != nil {
			return err
		}
	}

	sim, err := client.Simulate([]vtex.SimulationItemRequest{
		{ID: c.SKU, Quantity: c.Qty, Seller: seller},
	}, cep)
	if err != nil {
		return err
	}
	if len(sim.LogisticsInfo) == 0 || len(sim.LogisticsInfo[0].SLAs) == 0 {
		return errfmt.Domain(fmt.Sprintf("no delivery available to CEP %s", cep))
	}

	if g.Formatter().IsJSON() {
		return g.Formatter().Print(sim)
	}
	for _, sla := range sim.LogisticsInfo[0].SLAs {
		price := "free"
		if sla.Price > 0 {
			price = sla.Price.String()
		}
		fmt.Printf("%s  %s  (%d windows)\n", sla.Name, price, len(sla.AvailableDeliveryWindows))
		if err := printWindows(g, sla.AvailableDeliveryWindows, c.Windows); err != nil {
			return err
		}
	}
	return nil
}
