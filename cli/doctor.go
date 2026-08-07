package cli

import (
	"fmt"

	"github.com/voska/vtexkit/cli/errfmt"
)

// DoctorCmd answers one question: can this CLI place an order right now,
// and if not, what exactly does the user need to do?
//
// It exists because the failure modes are not guessable. A cart that cannot
// check out, an account with no CPF, a card that VTEX will not expose until
// after a first order — each produces a different downstream error, and an
// agent should not have to reason from those errors back to a cause.
type DoctorCmd struct{}

// probeQueries are common Portuguese food words. Stores are specialized —
// a fish shop has no "agua" — so several are tried, and matching a real
// product is better evidence that search works than a bare HTTP 200.
var probeQueries = []string{"agua", "sal", "peixe", "carne", "leite"}

const probeQuery = "agua"

type checkResult struct {
	Name   string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func (c *DoctorCmd) Run(g *Globals) error {
	var checks []checkResult
	add := func(name string, ok bool, detail, fix string) {
		checks = append(checks, checkResult{Name: name, OK: ok, Detail: detail, Fix: fix})
	}

	// 1. Can we reach the store at all?
	caps, err := g.Client().AuthStart()
	if err != nil {
		add("store reachable", false, err.Error(), "check your network connection")
		return c.report(g, checks)
	}
	add("store reachable", true, g.Store.BaseURL, "")

	// 2. Search, which needs no login. A query returning nothing is a fine
	// answer; only a transport or parse failure is a broken catalog.
	var probeErr error
	matched := ""
	for _, q := range probeQueries {
		results, sErr := g.Client().Search(q, 1)
		if sErr != nil {
			probeErr = sErr
			break
		}
		if len(results) > 0 {
			matched = results[0].Name
			break
		}
	}
	switch {
	case probeErr != nil:
		add("catalog search", false, probeErr.Error(),
			fmt.Sprintf("run: %s store info", g.Store.Name))
	case matched == "":
		add("catalog search", true, "reachable, no probe term matched", "")
	default:
		add("catalog search", true, matched, "")
	}

	// 3. Login.
	client, authErr := g.RequireAuth()
	if authErr != nil {
		how := fmt.Sprintf("%s auth login --email you@example.com", g.Store.Name)
		if !caps.Classic && caps.AccessKey {
			how = fmt.Sprintf("%s auth code send --email you@example.com", g.Store.Name)
		}
		add("logged in", false, authErr.Error(), "run: "+how)
		return c.report(g, checks)
	}
	user, _ := client.AuthenticatedUser()
	add("logged in", true, user, "")

	// 4. A cart that can actually complete a checkout. UsableCart already
	// self-heals a stale one, so a failure here means the account itself
	// is missing an address.
	of, migrated, cartErr := client.UsableCart(g.OrderFormID(client))
	if cartErr != nil {
		add("cart", false, cartErr.Error(), "")
		return c.report(g, checks)
	}
	if migrated {
		g.PersistOrderFormID(of.OrderFormID)
	}

	if of.Checkoutable() {
		add("delivery address", true, fmt.Sprintf("%d on file", of.AddressCount), "")
	} else {
		add("delivery address", false, "none on this account",
			"add a delivery address at "+g.Store.BaseURL)
	}

	// 5. Payment. Saved cards only appear once a cart has items, and only
	// after the account's first completed order.
	switch {
	case len(of.Items) == 0:
		add("saved cards", true, "cart empty — add an item to check",
			fmt.Sprintf("run: %s cart add <sku>", g.Store.Name))
	default:
		cards, _ := client.GetSavedCards(of.OrderFormID)
		if len(cards) > 0 {
			add("saved cards", true,
				fmt.Sprintf("%s %s", cards[0].PaymentSystemName, cards[0].CardNumber), "")
		} else {
			add("saved cards", false, "none visible to checkout",
				"VTEX only exposes a saved card after one completed order — place your first order on the website, or pay with pix")
		}
	}

	// 6. Order history proves the account has been used end to end.
	orders, ordErr := client.ListOrders()
	switch {
	case ordErr != nil:
		add("order history", false, ordErr.Error(), "")
	case len(orders) == 0:
		add("order history", false, "no orders yet",
			"place one order on "+g.Store.BaseURL+" to populate profile, address, and card")
	default:
		add("order history", true, fmt.Sprintf("%d orders", len(orders)), "")
	}

	return c.report(g, checks)
}

func (c *DoctorCmd) report(g *Globals, checks []checkResult) error {
	ready := true
	for _, ck := range checks {
		if !ck.OK {
			ready = false
		}
	}

	if g.Formatter().IsJSON() {
		if err := g.Formatter().Print(map[string]any{
			"store":  g.Store.Name,
			"ready":  ready,
			"checks": checks,
		}); err != nil {
			return err
		}
		if !ready {
			return errfmt.New(errfmt.ExitConfig, "not ready to order")
		}
		return nil
	}

	for _, ck := range checks {
		mark := "ok  "
		if !ck.OK {
			mark = "FAIL"
		}
		fmt.Printf("%-5s %-20s %s\n", mark, ck.Name, ck.Detail)
		if ck.Fix != "" && !ck.OK {
			fmt.Printf("      %-20s -> %s\n", "", ck.Fix)
		}
	}
	fmt.Println()
	if ready {
		fmt.Printf("Ready to order. Next: %s cart add <sku>, then %s checkout\n",
			g.Store.Name, g.Store.Name)
		return nil
	}
	return errfmt.New(errfmt.ExitConfig, "not ready to order — see the fixes above")
}
