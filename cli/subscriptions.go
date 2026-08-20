package cli

import (
	"fmt"
	"time"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/vtex"
)

// SubsCmd is the shopper's view of VTEX Subscriptions. Cancelling is absent
// on purpose: RNS has no transition out of CANCELED, and a CLI should not
// offer a one-keystroke path to a state it cannot undo.
type SubsCmd struct {
	Show   SubsShowCmd   `cmd:"" default:"withargs" help:"List subscriptions, or show one by ID."`
	Pause  SubsPauseCmd  `cmd:"" help:"Pause a subscription indefinitely."`
	Resume SubsResumeCmd `cmd:"" help:"Resume a paused subscription."`
	Skip   SubsSkipCmd   `cmd:"" help:"Skip the next delivery only."`
	Unskip SubsUnskipCmd `cmd:"" help:"Undo a skip and restore the next delivery."`
}

type SubsShowCmd struct {
	ID string `arg:"" optional:"" help:"Show one subscription's schedule and items."`
}

func (c *SubsShowCmd) Run(g *Globals) error {
	client, err := g.RequireAuth()
	if err != nil {
		return err
	}

	if c.ID != "" {
		if err := validateID(c.ID); err != nil {
			return err
		}
		sub, err := client.GetSubscription(c.ID)
		if err != nil {
			return err
		}
		if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
			return g.Formatter().Print(sub)
		}
		printSubscriptionDetail(g, client, sub)
		return nil
	}

	subs, err := client.ListSubscriptions()
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return errfmt.Empty()
	}
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(subs)
	}
	for _, s := range subs {
		fmt.Printf("%-34s %-8s %-16s next %-12s %d item(s)%s\n",
			s.ID, s.Status, s.Plan.Frequency, shortDate(s.NextPurchaseDate),
			len(s.Items), skipNote(s.IsSkipped))
	}
	return nil
}

// printSubscriptionDetail resolves SKU names for the human view only. An
// agent reading --json gets the SKU and can look it up itself; spending a
// catalog request per item to decorate machine output would be latency for
// nobody's benefit.
func printSubscriptionDetail(g *Globals, client *vtex.Client, s *vtex.Subscription) {
	fmt.Printf("%-10s %s\n", "id", s.ID)
	if s.Title != "" {
		fmt.Printf("%-10s %s\n", "title", s.Title)
	}
	fmt.Printf("%-10s %s%s\n", "status", s.Status, skipNote(s.IsSkipped))
	fmt.Printf("%-10s %s\n", "frequency", s.Plan.Frequency)
	fmt.Printf("%-10s %s\n", "next", shortDate(s.NextPurchaseDate))
	fmt.Printf("%-10s %s\n", "last", shortDate(s.LastPurchaseDate))
	if s.Settings.DeliveryWindow != "" {
		fmt.Printf("%-10s %s\n", "delivery", s.Settings.DeliveryWindow)
	}
	fmt.Printf("%-10s %d\n", "cycles", s.CycleCount)

	for _, item := range s.Items {
		name := item.SKU
		if found, err := lookupSKU(client, item.SKU); err == nil {
			name = found.Name
		}
		fmt.Printf("  %-8s %-44s x%-3d %10s%s\n",
			item.SKU, truncate(name, 44), item.Quantity, item.Price,
			skipNote(item.IsSkipped))
	}
}

// shortDate trims an RFC3339 timestamp to the day, which is the only part of
// a delivery date a shopper acts on. Unparseable values pass through rather
// than becoming a misleading zero date.
func shortDate(ts string) string {
	if ts == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02")
}

func skipNote(skipped bool) string {
	if skipped {
		return "  (next skipped)"
	}
	return ""
}

// subsWrite is the shared body of the four state-changing commands: validate,
// apply, then report the state the store actually stored rather than the one
// we asked for.
func subsWrite(g *Globals, id string, apply func(*vtex.Client) (*vtex.Subscription, error), done string) error {
	if err := validateID(id); err != nil {
		return err
	}
	client, err := g.RequireAuth()
	if err != nil {
		return err
	}
	sub, err := apply(client)
	if err != nil {
		return err
	}
	if g.Formatter().IsJSON() || g.CLI.Plain || g.CLI.Quiet {
		return g.Formatter().Print(sub)
	}
	fmt.Printf("%s — %s, next %s%s\n",
		done, sub.Status, shortDate(sub.NextPurchaseDate), skipNote(sub.IsSkipped))
	return nil
}

type SubsPauseCmd struct {
	ID string `arg:"" help:"Subscription ID."`
}

func (c *SubsPauseCmd) Run(g *Globals) error {
	return subsWrite(g, c.ID, func(cl *vtex.Client) (*vtex.Subscription, error) {
		return cl.SetSubscriptionStatus(c.ID, vtex.SubscriptionPaused)
	}, "paused")
}

type SubsResumeCmd struct {
	ID string `arg:"" help:"Subscription ID."`
}

func (c *SubsResumeCmd) Run(g *Globals) error {
	return subsWrite(g, c.ID, func(cl *vtex.Client) (*vtex.Subscription, error) {
		return cl.SetSubscriptionStatus(c.ID, vtex.SubscriptionActive)
	}, "resumed")
}

type SubsSkipCmd struct {
	ID string `arg:"" help:"Subscription ID."`
}

func (c *SubsSkipCmd) Run(g *Globals) error {
	return subsWrite(g, c.ID, func(cl *vtex.Client) (*vtex.Subscription, error) {
		return cl.SkipSubscription(c.ID, true)
	}, "skipped next delivery")
}

type SubsUnskipCmd struct {
	ID string `arg:"" help:"Subscription ID."`
}

func (c *SubsUnskipCmd) Run(g *Globals) error {
	return subsWrite(g, c.ID, func(cl *vtex.Client) (*vtex.Subscription, error) {
		return cl.SkipSubscription(c.ID, false)
	}, "restored next delivery")
}
