package vtex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/money"
)

// Subscription statuses reported by the RNS API. CANCELED is terminal, which
// is why this package exposes no way to reach it.
const (
	SubscriptionActive   = "ACTIVE"
	SubscriptionPaused   = "PAUSED"
	SubscriptionCanceled = "CANCELED"
	SubscriptionExpired  = "EXPIRED"
	SubscriptionMissing  = "MISSING"
)

const (
	// subscriptionsPageSize is RNS's page size for the subscription list.
	// The API's size parameter both defaults to 15 and caps at 15, so this
	// cannot be raised — only paged through.
	subscriptionsPageSize = 15
	// maxSubscriptionPages bounds the paging loop so a server that keeps
	// returning full pages cannot spin forever.
	maxSubscriptionPages = 100
)

// Frequency is how often a subscription reorders: interval 2 with
// periodicity WEEKLY means every two weeks.
type Frequency struct {
	Periodicity string `json:"periodicity"`
	Interval    int    `json:"interval"`
}

// String renders the frequency the way a shopper reads it.
func (f Frequency) String() string {
	unit := map[string]string{
		"DAILY":   "day",
		"WEEKLY":  "week",
		"MONTHLY": "month",
		"YEARLY":  "year",
	}[f.Periodicity]
	if unit == "" {
		return f.Periodicity
	}
	if f.Interval <= 1 {
		return "every " + unit
	}
	return fmt.Sprintf("every %d %ss", f.Interval, unit)
}

// SubscriptionSettings carries the only human-readable part of the API's
// purchaseSettings; the rest is payment and address IDs.
type SubscriptionSettings struct {
	DeliveryWindow string `json:"selectedSla"`
}

type SubscriptionPlan struct {
	ID        string    `json:"id"`
	Frequency Frequency `json:"frequency"`
}

// SubscriptionItem is one SKU in a subscription. A subscription can carry
// several, and each can be skipped independently of the subscription itself.
//
// Price is the price locked in when the subscription was created, which is
// why it keeps the API's name rather than becoming a plain "price" — it is
// not today's shelf price. RNS sends it as integer centavos serialized as a
// float (6590.0 for R$65,90), the same encoding the OMS uses, so
// money.Centavos reads it correctly with no conversion.
type SubscriptionItem struct {
	ID        string         `json:"id"`
	SKU       string         `json:"skuId"`
	Quantity  int            `json:"quantity"`
	Status    string         `json:"status"`
	IsSkipped bool           `json:"isSkipped"`
	Price     money.Centavos `json:"priceAtSubscriptionDate"`
}

// Subscription is one recurring order.
//
// The address and payment method RNS reports are opaque VTEX IDs with nothing
// a shopper could read, so they are not surfaced. DeliveryWindow is the one
// human-readable part of purchaseSettings — Mantiqueira returns "Receba às
// terças-feiras".
type Subscription struct {
	ID               string               `json:"id"`
	CustomerEmail    string               `json:"customerEmail"`
	Title            string               `json:"title"`
	Status           string               `json:"status"`
	IsSkipped        bool                 `json:"isSkipped"`
	NextPurchaseDate string               `json:"nextPurchaseDate"`
	LastPurchaseDate string               `json:"lastPurchaseDate"`
	CycleCount       int                  `json:"cycleCount"`
	Plan             SubscriptionPlan     `json:"plan"`
	Settings         SubscriptionSettings `json:"purchaseSettings"`
	Items            []SubscriptionItem   `json:"items"`
}

// subscriptionsURL builds an RNS URL on the account host.
func (c *Client) subscriptionsURL(path string) string {
	base := c.SubscriptionsURL
	if base == "" {
		base = c.store.AccountBaseURL()
	}
	return base + "/api/rns/pub/subscriptions" + path
}

// ListSubscriptions returns the shopper's subscriptions.
//
// The endpoint takes a customerEmail filter, but a shopper token already
// scopes the result to its own customer — verified against a live account —
// so passing one would only cost an extra round trip to learn our own email.
func (c *Client) ListSubscriptions() ([]Subscription, error) {
	var all []Subscription
	for page := 1; page <= maxSubscriptionPages; page++ {
		q := url.Values{"page": {strconv.Itoa(page)}}
		body, err := c.getAbsolute(c.subscriptionsURL("?" + q.Encode()))
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		// RNS returns a bare array, unlike the OMS list's {"list":[…]}.
		var batch []Subscription
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("list subscriptions parse: %w", err)
		}
		all = append(all, batch...)
		if len(batch) < subscriptionsPageSize {
			return all, nil
		}
	}
	// Never truncate quietly: a short list that looks complete is worse than
	// a failure, because nothing downstream can tell the difference.
	return nil, fmt.Errorf(
		"list subscriptions: more than %d subscriptions; refusing to return a partial list",
		maxSubscriptionPages*subscriptionsPageSize)
}

func (c *Client) GetSubscription(id string) (*Subscription, error) {
	body, err := c.getAbsolute(c.subscriptionsURL("/" + url.PathEscape(id)))
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	var sub Subscription
	if err := json.Unmarshal(body, &sub); err != nil {
		return nil, fmt.Errorf("get subscription parse: %w", err)
	}
	return &sub, nil
}

// patchSubscription applies a partial update and returns the stored result.
func (c *Client) patchSubscription(id string, patch map[string]any) (*Subscription, error) {
	body, err := c.sendJSON(http.MethodPatch, c.subscriptionsURL("/"+url.PathEscape(id)), patch)
	if err != nil {
		return nil, err
	}
	// A PATCH that changes nothing — resuming an already-active subscription,
	// unskipping one that was not skipped — answers 304 with an empty body,
	// which the OpenAPI spec does not mention. That is a success, not a
	// no-op error, so read the current state back and report it.
	if len(body) == 0 {
		return c.GetSubscription(id)
	}
	var sub Subscription
	if err := json.Unmarshal(body, &sub); err != nil {
		return nil, fmt.Errorf("update subscription parse: %w", err)
	}
	return &sub, nil
}

// SetSubscriptionStatus moves a subscription between ACTIVE and PAUSED.
//
// CANCELED is deliberately refused. RNS has no transition out of it, so a
// mistyped ID would destroy a subscription with no way back from this CLI.
func (c *Client) SetSubscriptionStatus(id, status string) (*Subscription, error) {
	switch status {
	case SubscriptionActive, SubscriptionPaused:
	case SubscriptionCanceled:
		return nil, errfmt.Domain(
			"cancelling is not supported here — it cannot be undone; use the store's website")
	default:
		return nil, errfmt.Usage(fmt.Sprintf("unknown subscription status %q", status))
	}
	sub, err := c.patchSubscription(id, map[string]any{"status": status})
	if err != nil {
		return nil, fmt.Errorf("set subscription status: %w", err)
	}
	return sub, nil
}

// SkipSubscription skips the next cycle, or restores it when skip is false.
func (c *Client) SkipSubscription(id string, skip bool) (*Subscription, error) {
	sub, err := c.patchSubscription(id, map[string]any{"isSkipped": skip})
	if err != nil {
		return nil, fmt.Errorf("skip subscription: %w", err)
	}
	return sub, nil
}
