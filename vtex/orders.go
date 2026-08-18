package vtex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/money"
)

// settlementAttempts bounds how many times AwaitOrderSettlement re-reads an
// order. With the default interval that is a little under half a minute —
// long enough for a gateway that answers, short enough that a CLI still
// returns while the caller is watching.
const settlementAttempts = 8

const defaultSettlementInterval = 3 * time.Second

type Order struct {
	OrderID           string         `json:"orderId"`
	CreationDate      string         `json:"creationDate"`
	Status            string         `json:"status"`
	StatusDescription string         `json:"statusDescription"`
	TotalValue        money.Centavos `json:"totalValue"`
	TotalItems        int            `json:"totalItems"`
}

type OrderItem struct {
	ID       string         `json:"id"`
	SKU      string         `json:"sellerSku"`
	Name     string         `json:"name"`
	Quantity int            `json:"quantity"`
	Price    money.Centavos `json:"price"`
}

// OrderPayment is one payment on a placed order, as the store reports it
// afterwards. Card and account identifiers are deliberately not modeled:
// nothing this CLI does needs them, and what is not parsed cannot leak.
type OrderPayment struct {
	ID                 string         `json:"id"`
	PaymentSystemName  string         `json:"paymentSystemName"`
	Group              string         `json:"group"`
	Value              money.Centavos `json:"value"`
	TID                string         `json:"tid"`
	ConnectorResponses map[string]any `json:"connectorResponses,omitempty"`
}

// OrderDetail is one order as the store reports it after the fact.
//
// Authorized and TID are derived at parse time because they answer the only
// question worth asking once an order exists: did money actually move. On
// 2026-08-18 a Frescatto order printed as placed while every payment on it
// carried a null tid and an empty connector response; the gateway cancelled
// it five minutes later.
type OrderDetail struct {
	OrderID           string         `json:"orderId"`
	Status            string         `json:"status"`
	StatusDescription string         `json:"statusDescription,omitempty"`
	CreationDate      string         `json:"creationDate,omitempty"`
	AuthorizedDate    string         `json:"authorizedDate,omitempty"`
	Value             money.Centavos `json:"value"`
	Authorized        bool           `json:"authorized"`
	TID               string         `json:"tid,omitempty"`
	Payments          []OrderPayment `json:"payments,omitempty"`
	Items             []OrderItem    `json:"items"`
}

// Canceled reports whether the store has cancelled this order or started to.
// Every VTEX cancellation status carries the word.
func (o *OrderDetail) Canceled() bool { return strings.Contains(o.Status, "cancel") }

// orderDetailWire is the wire shape. Payments are nested two levels down
// under paymentData on the wire but belong to the order for any caller.
type orderDetailWire struct {
	OrderID           string         `json:"orderId"`
	Status            string         `json:"status"`
	StatusDescription string         `json:"statusDescription"`
	CreationDate      string         `json:"creationDate"`
	AuthorizedDate    string         `json:"authorizedDate"`
	Value             money.Centavos `json:"value"`
	Items             []OrderItem    `json:"items"`
	PaymentData       struct {
		Transactions []struct {
			Payments []OrderPayment `json:"payments"`
		} `json:"transactions"`
	} `json:"paymentData"`
}

func (w orderDetailWire) toDetail() *OrderDetail {
	detail := &OrderDetail{
		OrderID:           w.OrderID,
		Status:            w.Status,
		StatusDescription: w.StatusDescription,
		CreationDate:      w.CreationDate,
		AuthorizedDate:    w.AuthorizedDate,
		Value:             w.Value,
		Items:             w.Items,
	}
	for _, tx := range w.PaymentData.Transactions {
		detail.Payments = append(detail.Payments, tx.Payments...)
	}
	// A settled payment carries the acquirer's transaction id. An order
	// the store has authorized carries a date. Either one is money moved;
	// neither one is the unsettled shape.
	for _, p := range detail.Payments {
		if p.TID != "" {
			detail.TID = p.TID
			break
		}
	}
	detail.Authorized = detail.AuthorizedDate != "" || detail.TID != ""
	return detail
}

func (c *Client) ListOrders() ([]Order, error) {
	body, err := c.Get("/api/oms/user/orders")
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	var resp struct {
		List []Order `json:"list"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("list orders parse: %w", err)
	}
	return resp.List, nil
}

// GetOrder reads one order.
//
// Checkout reports the order *group*, which is the id a caller actually
// holds, while OMS keys orders as "<group>-<seq>". The id is tried exactly
// as given first, so a full order id behaves as it always has; the fallback
// only makes the id the CLI printed usable.
func (c *Client) GetOrder(orderID string) (*OrderDetail, error) {
	detail, err := c.getOrder(orderID)
	if err == nil {
		return detail, nil
	}
	var typed *errfmt.Error
	if errors.As(err, &typed) && typed.Code == errfmt.ExitNotFound && !strings.Contains(orderID, "-") {
		if first, retryErr := c.getOrder(orderID + "-01"); retryErr == nil {
			return first, nil
		}
	}
	return nil, err
}

func (c *Client) getOrder(orderID string) (*OrderDetail, error) {
	body, err := c.Get(fmt.Sprintf("/api/oms/user/orders/%s", orderID))
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	var wire orderDetailWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("get order parse: %w", err)
	}
	return wire.toDetail(), nil
}

func (c *Client) settlementInterval() time.Duration {
	if c.SettlementInterval > 0 {
		return c.SettlementInterval
	}
	return defaultSettlementInterval
}

// AwaitOrderSettlement re-reads an order until the gateway has settled a
// payment on it, the store has cancelled it, or the poll budget runs out.
//
// Running out is an answer, not an error: the caller gets the last state the
// store reported and must say so rather than claim a placed order. Only an
// order that could not be read at all is an error, because then there is
// nothing true to report.
func (c *Client) AwaitOrderSettlement(orderGroup string) (*OrderDetail, error) {
	var (
		last    *OrderDetail
		lastErr error
	)
	for attempt := range settlementAttempts {
		if attempt > 0 {
			time.Sleep(c.settlementInterval())
		}
		detail, err := c.GetOrder(orderGroup)
		if err != nil {
			// A just-placed order can be briefly invisible to OMS.
			lastErr = err
			continue
		}
		last = detail
		if detail.Authorized || detail.Canceled() {
			return detail, nil
		}
	}
	if last == nil {
		return nil, fmt.Errorf("read back order %s: %w", orderGroup, lastErr)
	}
	return last, nil
}
