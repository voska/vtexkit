package vtex

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/voska/vtexkit/money"
)

type DeliveryWindow struct {
	Index    int            `json:"index"`
	Start    time.Time      `json:"start"`
	End      time.Time      `json:"end"`
	Price    money.Centavos `json:"price"`
	LisPrice money.Centavos `json:"lisPrice"`
	Tax      money.Centavos `json:"tax"`
	// RawStart and RawEnd preserve the exact strings VTEX sent. The
	// shipping-window request must echo them back byte for byte; a
	// re-formatted timestamp is rejected.
	RawStart string `json:"-"`
	RawEnd   string `json:"-"`
}

type SimulationItemRequest struct {
	ID       string `json:"id"`
	Quantity int    `json:"quantity"`
	Seller   string `json:"seller"`
}

type SimulationItem struct {
	ID       string         `json:"id"`
	Quantity int            `json:"quantity"`
	Price    money.Centavos `json:"price"`
}

type SimulationSLA struct {
	Name                     string           `json:"name"`
	Price                    money.Centavos   `json:"price"`
	ShippingEstimate         string           `json:"shippingEstimate"`
	AvailableDeliveryWindows []DeliveryWindow `json:"availableDeliveryWindows"`
}

type SimulationLogisticsInfo struct {
	SLAs []SimulationSLA `json:"slas"`
}

type Simulation struct {
	Items          []SimulationItem          `json:"items"`
	LogisticsInfo  []SimulationLogisticsInfo `json:"logisticsInfo"`
	PaymentSystems []PaymentSystem           `json:"paymentSystems"`
}

// rawWindow is the wire shape of a delivery window.
type rawWindow struct {
	StartDateUtc string         `json:"startDateUtc"`
	EndDateUtc   string         `json:"endDateUtc"`
	Price        money.Centavos `json:"price"`
	LisPrice     money.Centavos `json:"lisPrice"`
	Tax          money.Centavos `json:"tax"`
}

// windowKey identifies a delivery window by the exact timestamps VTEX
// issued. Deduplicating the window list and matching a window back to the
// SLA that owns it have to agree on what "the same window" means, so both
// go through here.
func windowKey(start, end string) string { return start + "|" + end }

func (r rawWindow) toWindow(index int) DeliveryWindow {
	start, _ := time.Parse(time.RFC3339, r.StartDateUtc)
	end, _ := time.Parse(time.RFC3339, r.EndDateUtc)
	return DeliveryWindow{
		Index:    index,
		Start:    start,
		End:      end,
		Price:    r.Price,
		LisPrice: r.LisPrice,
		Tax:      r.Tax,
		RawStart: r.StartDateUtc,
		RawEnd:   r.EndDateUtc,
	}
}

// GetDeliveryWindows lists the windows available for the cart's address.
// Requires an orderForm with a shipping address, so it needs authentication;
// use Simulate for an unauthenticated check.
//
// Windows are deduplicated across items and reindexed from zero, because
// `--window N` indexes into the returned slice.
func (c *Client) GetDeliveryWindows(orderFormID string) ([]DeliveryWindow, error) {
	path := "/api/checkout/pub/orderForm"
	if orderFormID != "" {
		path = fmt.Sprintf("/api/checkout/pub/orderForm/%s", orderFormID)
	}
	body, err := c.Get(path)
	if err != nil {
		return nil, fmt.Errorf("get delivery windows: %w", err)
	}

	var of struct {
		ShippingData struct {
			LogisticsInfo []struct {
				SLAs []struct {
					ID                       string      `json:"id"`
					AvailableDeliveryWindows []rawWindow `json:"availableDeliveryWindows"`
				} `json:"slas"`
			} `json:"logisticsInfo"`
		} `json:"shippingData"`
	}
	if err := json.Unmarshal(body, &of); err != nil {
		return nil, fmt.Errorf("delivery windows parse: %w", err)
	}

	seen := map[string]bool{}
	var windows []DeliveryWindow
	for _, li := range of.ShippingData.LogisticsInfo {
		for _, sla := range li.SLAs {
			for _, dw := range sla.AvailableDeliveryWindows {
				key := windowKey(dw.StartDateUtc, dw.EndDateUtc)
				if seen[key] {
					continue
				}
				seen[key] = true
				windows = append(windows, dw.toWindow(len(windows)))
			}
		}
	}
	return windows, nil
}

// Simulate prices a basket against a postal code without authentication.
// This is how delivery windows and payment methods can be inspected before
// anyone logs in.
func (c *Client) Simulate(items []SimulationItemRequest, cep string) (*Simulation, error) {
	payload := map[string]any{
		"items":      items,
		"postalCode": cep,
		"country":    "BRA",
	}
	body, err := c.PostJSON("/api/checkout/pub/orderForms/simulation", payload)
	if err != nil {
		return nil, fmt.Errorf("simulate delivery: %w", err)
	}

	var raw struct {
		Items         []SimulationItem `json:"items"`
		LogisticsInfo []struct {
			SLAs []struct {
				ID                       string         `json:"id"`
				Name                     string         `json:"name"`
				Price                    money.Centavos `json:"price"`
				ShippingEstimate         string         `json:"shippingEstimate"`
				AvailableDeliveryWindows []rawWindow    `json:"availableDeliveryWindows"`
			} `json:"slas"`
		} `json:"logisticsInfo"`
		PaymentData struct {
			PaymentSystems []PaymentSystem `json:"paymentSystems"`
		} `json:"paymentData"`
		Messages []struct {
			Code   string `json:"code"`
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("simulate parse: %w", err)
	}

	sim := &Simulation{
		Items:          raw.Items,
		PaymentSystems: raw.PaymentData.PaymentSystems,
	}
	for _, li := range raw.LogisticsInfo {
		info := SimulationLogisticsInfo{}
		for _, sla := range li.SLAs {
			name := sla.Name
			if name == "" {
				name = sla.ID
			}
			s := SimulationSLA{
				Name:             name,
				Price:            sla.Price,
				ShippingEstimate: sla.ShippingEstimate,
			}
			for i, dw := range sla.AvailableDeliveryWindows {
				s.AvailableDeliveryWindows = append(s.AvailableDeliveryWindows, dw.toWindow(i))
			}
			info.SLAs = append(info.SLAs, s)
		}
		sim.LogisticsInfo = append(sim.LogisticsInfo, info)
	}
	return sim, nil
}
