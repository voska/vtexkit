package vtex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/voska/vtexkit/cli/errfmt"
	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
)

type shippingInfo struct {
	SelectedAddresses []map[string]any
	AddressID         string
	SLAName           string
	NumItems          int
	// SLAs are the delivery options the store offered for this address,
	// in the order it listed them, each with the windows it owns. VTEX
	// validates a requested window against the selected SLA, so the two
	// can only be chosen together.
	SLAs []slaOption
}

// slaOption is one delivery option and the set of windows it offers.
type slaOption struct {
	ID      string
	Windows map[string]bool
}

// slaForWindow returns the SLA offering this window, or "" when none does.
// A store lists its scheduled option alongside its standard one and only the
// scheduled one carries windows, so the first SLA is usually the wrong one.
func (s *shippingInfo) slaForWindow(w DeliveryWindow) string {
	key := windowKey(w.RawStart, w.RawEnd)
	for _, opt := range s.SLAs {
		if opt.Windows[key] {
			return opt.ID
		}
	}
	return ""
}

// getShippingInfo reads the address and SLA already on the cart. The address
// comes from the account, so it is never constructed here.
func (c *Client) getShippingInfo(orderFormID string) (*shippingInfo, error) {
	body, err := c.Get(fmt.Sprintf("/api/checkout/pub/orderForm/%s", orderFormID))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items        []any `json:"items"`
		ShippingData struct {
			Address            map[string]any   `json:"address"`
			SelectedAddresses  []map[string]any `json:"selectedAddresses"`
			AvailableAddresses []map[string]any `json:"availableAddresses"`
			LogisticsInfo      []struct {
				SLAs []struct {
					ID                       string `json:"id"`
					AvailableDeliveryWindows []struct {
						StartDateUtc string `json:"startDateUtc"`
						EndDateUtc   string `json:"endDateUtc"`
					} `json:"availableDeliveryWindows"`
				} `json:"slas"`
			} `json:"logisticsInfo"`
		} `json:"shippingData"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("shipping info parse: %w", err)
	}

	info := &shippingInfo{NumItems: len(resp.Items)}

	var address map[string]any
	switch {
	case len(resp.ShippingData.SelectedAddresses) > 0:
		address = resp.ShippingData.SelectedAddresses[0]
	case resp.ShippingData.Address != nil:
		address = resp.ShippingData.Address
	case len(resp.ShippingData.AvailableAddresses) > 0:
		address = resp.ShippingData.AvailableAddresses[0]
	}
	if address != nil {
		info.SelectedAddresses = []map[string]any{address}
		if id, ok := address["addressId"].(string); ok {
			info.AddressID = id
		}
	}

	if len(resp.ShippingData.LogisticsInfo) > 0 {
		for _, sla := range resp.ShippingData.LogisticsInfo[0].SLAs {
			opt := slaOption{ID: sla.ID, Windows: map[string]bool{}}
			for _, dw := range sla.AvailableDeliveryWindows {
				opt.Windows[windowKey(dw.StartDateUtc, dw.EndDateUtc)] = true
			}
			info.SLAs = append(info.SLAs, opt)
		}
	}
	if len(info.SLAs) > 0 {
		info.SLAName = info.SLAs[0].ID
	}
	return info, nil
}

// buildLogistics assembles the per-item logistics payload. window is optional.
func buildLogistics(info *shippingInfo, numItems int, window *DeliveryWindow) []map[string]any {
	out := make([]map[string]any, numItems)
	for i := range numItems {
		entry := map[string]any{
			"itemIndex":               i,
			"addressId":               info.AddressID,
			"selectedSla":             info.SLAName,
			"selectedDeliveryChannel": "delivery",
		}
		if window != nil {
			// VTEX matches windows on the exact strings it issued, so the
			// raw timestamps are echoed rather than reformatted.
			entry["deliveryWindow"] = map[string]any{
				"startDateUtc": window.RawStart,
				"endDateUtc":   window.RawEnd,
				"price":        window.Price,
				"lisPrice":     window.LisPrice,
				"tax":          window.Tax,
			}
		}
		out[i] = entry
	}
	return out
}

// postShippingData applies the logistics payload and hands back whatever
// VTEX answered with. That response is the only evidence that what was
// requested is what stuck, so it is returned rather than discarded.
func (c *Client) postShippingData(orderFormID string, logistics []map[string]any, addresses []map[string]any) ([]byte, error) {
	payload := map[string]any{
		"logisticsInfo":                    logistics,
		"selectedAddresses":                addresses,
		"clearAddressIfPostalCodeNotFound": false,
	}
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/attachments/shippingData", orderFormID)
	return c.PostJSON(path, payload)
}

// logisticsEcho is the slice of the shipping response that says what VTEX
// actually bound.
type logisticsEcho struct {
	SelectedSLA    string `json:"selectedSla"`
	DeliveryWindow *struct {
		StartDateUtc string `json:"startDateUtc"`
		EndDateUtc   string `json:"endDateUtc"`
	} `json:"deliveryWindow"`
}

// confirmWindow checks the shipping response against the window that was
// requested.
//
// It refuses only on positive evidence of a different window. A store that
// echoes no window at all leaves the question unanswerable, and an
// unanswerable question must not fail a request VTEX accepted — the point
// here is to catch a silent swap, not to invent one.
func confirmWindow(body []byte, window DeliveryWindow) error {
	var resp struct {
		LogisticsInfo []logisticsEcho `json:"logisticsInfo"`
		ShippingData  struct {
			LogisticsInfo []logisticsEcho `json:"logisticsInfo"`
		} `json:"shippingData"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	entries := resp.ShippingData.LogisticsInfo
	if len(entries) == 0 {
		entries = resp.LogisticsInfo
	}

	want := windowKey(window.RawStart, window.RawEnd)
	for _, li := range entries {
		if li.DeliveryWindow == nil {
			continue
		}
		got := windowKey(li.DeliveryWindow.StartDateUtc, li.DeliveryWindow.EndDateUtc)
		if got != want {
			return errfmt.Domain(fmt.Sprintf(
				"store bound the delivery window starting %s, not the one requested (%s)",
				li.DeliveryWindow.StartDateUtc, window.RawStart))
		}
	}
	return nil
}

// SetAddress applies the account's saved delivery address to the cart.
func (c *Client) SetAddress(orderFormID string, numItems int) error {
	info, err := c.getShippingInfo(orderFormID)
	if err != nil {
		return fmt.Errorf("set address: %w", err)
	}
	if info.SelectedAddresses == nil {
		return errfmt.Domain("no delivery address on this account — add one on the website first")
	}
	// The SLA is whatever the store offers for this address. Guessing a
	// default (the previous code hardcoded "Entrega Zona Sul") silently
	// produces an invalid shipping request on any other store.
	if info.SLAName == "" {
		return errfmt.Domain("store offers no delivery option for this address")
	}
	if _, err := c.postShippingData(orderFormID, buildLogistics(info, numItems, nil), info.SelectedAddresses); err != nil {
		return fmt.Errorf("set address: %w", err)
	}
	return nil
}

// SetShippingWindow selects a delivery window for every item in the cart.
func (c *Client) SetShippingWindow(orderFormID string, window DeliveryWindow, numItems int) error {
	info, err := c.getShippingInfo(orderFormID)
	if err != nil {
		return fmt.Errorf("set shipping window: %w", err)
	}
	if info.SelectedAddresses == nil {
		return errfmt.Domain("no delivery address on this account — add one on the website first")
	}
	if info.SLAName == "" {
		return errfmt.Domain("store offers no delivery option for this address")
	}
	// The requested window belongs to exactly one of the store's delivery
	// options. Binding the first one instead is what Zona Sul rejects with
	// ORD006, and what silently prices a scheduled window as standard
	// delivery when it does go through.
	sla := info.slaForWindow(window)
	if sla == "" {
		return errfmt.Domain(fmt.Sprintf(
			"no delivery option offers the window starting %s — re-read them with: %s delivery windows",
			window.RawStart, c.store.Name))
	}
	info.SLAName = sla

	body, err := c.postShippingData(orderFormID, buildLogistics(info, numItems, &window), info.SelectedAddresses)
	if err != nil {
		return fmt.Errorf("set shipping window: %w", err)
	}
	return confirmWindow(body, window)
}

type SavedCard struct {
	AccountID         string `json:"accountId"`
	CardNumber        string `json:"cardNumber"`
	Bin               string `json:"bin"`
	PaymentSystem     string `json:"paymentSystem"`
	PaymentSystemName string `json:"paymentSystemName"`
}

func (c *Client) GetSavedCards(orderFormID string) ([]SavedCard, error) {
	body, err := c.Get(fmt.Sprintf("/api/checkout/pub/orderForm/%s", orderFormID))
	if err != nil {
		return nil, fmt.Errorf("get saved cards: %w", err)
	}
	var resp struct {
		PaymentData struct {
			AvailableAccounts []SavedCard `json:"availableAccounts"`
		} `json:"paymentData"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("get saved cards parse: %w", err)
	}
	return resp.PaymentData.AvailableAccounts, nil
}

// ResolvePaymentSystem maps a human name such as "pix" to the store's own
// payment system ID, discovered from the order form. IDs are not portable
// between stores, so this replaces the hardcoded map the CLIs used to carry.
func (c *Client) ResolvePaymentSystem(of *OrderForm, name string) (int, error) {
	if len(of.PaymentSystems) == 0 {
		return 0, errfmt.Domain(
			"store reported no payment methods — add an item to the cart first")
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, ps := range of.PaymentSystems {
		if strings.ToLower(ps.Name) == want {
			return ps.ID, nil
		}
	}
	// Listing what IS available is what lets an agent recover.
	available := make([]string, 0, len(of.PaymentSystems))
	for _, ps := range of.PaymentSystems {
		available = append(available, ps.Name)
	}
	return 0, errfmt.Usage(fmt.Sprintf(
		"payment method %q is not offered by this store; available: %s",
		name, strings.Join(available, ", ")))
}

func (c *Client) SetPayment(orderFormID string, paymentSystemID int, value money.Centavos) error {
	payload := map[string]any{
		"payments": []map[string]any{
			{
				"paymentSystem":  paymentSystemID,
				"referenceValue": value,
				"value":          value,
				"installments":   1,
			},
		},
	}
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/attachments/paymentData", orderFormID)
	if _, err := c.PostJSON(path, payload); err != nil {
		return fmt.Errorf("set payment: %w", err)
	}
	return nil
}

func (c *Client) SetPaymentWithSavedCard(orderFormID string, card SavedCard, value money.Centavos) error {
	psID := paymentSystemID(card)
	payload := map[string]any{
		"payments": []map[string]any{
			{
				"paymentSystem":     psID,
				"paymentSystemName": card.PaymentSystemName,
				"group":             "creditCardPaymentGroup",
				"installments":      1,
				"installmentsValue": value,
				"value":             value,
				"referenceValue":    value,
				"accountId":         card.AccountID,
				"tokenId":           nil,
			},
		},
	}
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/attachments/paymentData", orderFormID)
	if _, err := c.PostJSON(path, payload); err != nil {
		return fmt.Errorf("set payment with saved card: %w", err)
	}
	return nil
}

func paymentSystemID(card SavedCard) int {
	id := 2 // Visa, the most common default
	if card.PaymentSystem != "" {
		_, _ = fmt.Sscanf(card.PaymentSystem, "%d", &id)
	}
	return id
}

type TransactionResult struct {
	OrderGroup    string `json:"orderGroup"`
	TransactionID string `json:"transactionId"`
	ReceiverURI   string `json:"receiverUri"`
	MerchantName  string `json:"merchantName"`
}

// PlaceOrder converts the cart into a transaction. This is the point of no
// return for non-card payments.
func (c *Client) PlaceOrder(orderFormID string, orderValue money.Centavos) (*TransactionResult, error) {
	payload := map[string]any{
		"referenceId":      orderFormID,
		"savePersonalData": true,
		"optinNewsLetter":  false,
		"value":            orderValue,
		"referenceValue":   orderValue,
		"interestValue":    0,
	}
	path := fmt.Sprintf("/api/checkout/pub/orderForm/%s/transaction", orderFormID)
	body, err := c.PostJSON(path, payload)
	if err != nil {
		return nil, fmt.Errorf("place order: %w", err)
	}

	var resp struct {
		ID         string `json:"id"`
		OrderGroup string `json:"orderGroup"`
		Messages   []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"messages"`
		MerchantTransactions []struct {
			ID            string `json:"id"`
			TransactionID string `json:"transactionId"`
		} `json:"merchantTransactions"`
		ReceiverURI string `json:"receiverUri"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("place order parse: %w", err)
	}
	for _, msg := range resp.Messages {
		if msg.Status == "error" {
			return nil, errfmt.Domain("place order: " + msg.Text)
		}
	}

	result := &TransactionResult{
		OrderGroup:  resp.OrderGroup,
		ReceiverURI: resp.ReceiverURI,
	}
	if result.OrderGroup == "" {
		result.OrderGroup = resp.ID
	}
	if result.OrderGroup == "" {
		return nil, fmt.Errorf("place order: response carried no order ID")
	}
	if len(resp.MerchantTransactions) > 0 {
		result.TransactionID = resp.MerchantTransactions[0].TransactionID
		result.MerchantName = resp.MerchantTransactions[0].ID
	}
	return result, nil
}

// PayWithSavedCard submits a card payment to the VTEX payment gateway.
//
// ClearSale fingerprinting is applied only for stores carrying the
// ClearSaleFingerprint quirk. Zona Sul's gateway rejects card payments
// without it (Cielo code 59); Frescatto shows no sign of needing it.
func (c *Client) PayWithSavedCard(tx *TransactionResult, card SavedCard, cvv string, orderValue money.Centavos) error {
	if tx.MerchantName == "" {
		return fmt.Errorf("pay with saved card: merchant name missing from transaction")
	}
	if tx.TransactionID == "" {
		return fmt.Errorf("pay with saved card: transaction id missing")
	}

	fields := map[string]string{
		"validationCode": cvv,
		"securityCode":   cvv,
		"accountId":      card.AccountID,
		"bin":            card.Bin,
	}
	if c.store.Quirks.Has(store.ClearSaleFingerprint) {
		sid, err := GenerateClearSaleSession(c.httpClient, c.ClearSaleURL)
		if err != nil && sid == "" {
			return fmt.Errorf("pay with saved card: %w", err)
		}
		fields["deviceFingerprint"] = sid
	}

	merchantName := strings.Split(tx.MerchantName, "-")[0]
	payload := []map[string]any{
		{
			"paymentSystem":             paymentSystemID(card),
			"paymentSystemName":         card.PaymentSystemName,
			"group":                     "creditCardPaymentGroup",
			"installments":              1,
			"installmentsInterestRate":  0,
			"installmentsValue":         orderValue,
			"value":                     orderValue,
			"referenceValue":            orderValue,
			"accountId":                 card.AccountID,
			"fields":                    fields,
			"hasDefaultBillingAddress":  true,
			"isBillingAddressDifferent": false,
			"id":                        tx.MerchantName,
			"interestRate":              0,
			"installmentValue":          orderValue,
			"transaction": map[string]string{
				"id":           tx.TransactionID,
				"merchantName": merchantName,
			},
			"currencyCode":         "BRL",
			"originalPaymentIndex": 0,
		},
	}

	account := c.store.AccountName()
	paymentsURL := fmt.Sprintf("https://%s.vtexpayments.com.br/api/payments/pub/transactions/%s/payments",
		account, tx.TransactionID)
	if c.GatewayURL != "" {
		paymentsURL = fmt.Sprintf("%s/api/payments/pub/transactions/%s/payments",
			c.GatewayURL, tx.TransactionID)
	}
	callbackURL := fmt.Sprintf("%s/checkout/gatewayCallback/%s/{messageCode}",
		c.store.BaseURL, tx.OrderGroup)
	// Base64 screen/device data the gateway expects.
	deviceInfo := "c3c9MTkyMCZzaD0xMDgwJmNkPTI0JnR6PTE4MCZsYW5nPXB0LUJSJmphdmE9ZmFsc2U="

	gatewayURL := fmt.Sprintf("%s?&orderId=%s&redirect=false&callbackUrl=%s&deviceInfo=%s&an=%s",
		paymentsURL, tx.OrderGroup, url.QueryEscape(callbackURL), deviceInfo, account)

	if _, err := c.PostJSONAbsolute(gatewayURL, payload); err != nil {
		return fmt.Errorf("pay with saved card: %w", err)
	}
	return nil
}

// GatewayCallback finalizes a card payment. The gateway answers 428 while
// the transaction is still settling, so this retries with a linear backoff.
func (c *Client) GatewayCallback(orderGroup string) error {
	const maxAttempts = 10
	path := fmt.Sprintf("/api/checkout/pub/gatewayCallback/%s", orderGroup)

	for attempt := range maxAttempts {
		req, err := http.NewRequest(http.MethodPost, c.store.BaseURL+path, http.NoBody)
		if err != nil {
			return fmt.Errorf("gateway callback: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.authToken != "" {
			req.AddCookie(&http.Cookie{Name: c.store.AuthCookieName(), Value: c.authToken})
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("gateway callback: %w", err)
		}
		status := resp.StatusCode
		_ = resp.Body.Close()

		if status == http.StatusOK || status == http.StatusNoContent {
			return nil
		}
		if (status == http.StatusPreconditionRequired || status >= 500) && attempt < maxAttempts-1 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		return fmt.Errorf("gateway callback: HTTP %d", status)
	}
	return nil
}
