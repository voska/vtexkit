package vtex_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/vtex"
)

func TestSimulateWorksUnauthenticated(t *testing.T) {
	var gotBody struct {
		Items      []vtex.SimulationItemRequest `json:"items"`
		PostalCode string                       `json:"postalCode"`
		Country    string                       `json:"country"`
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("VtexIdclientAutCookie_testacct"); err == nil {
			t.Error("simulation must not require authentication")
		}
		if !strings.HasSuffix(r.URL.Path, "/orderForms/simulation") {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write(mustReadFile(t, "testdata/simulation.json"))
	})
	c.SetAuthToken("")

	sim, err := c.Simulate([]vtex.SimulationItemRequest{
		{ID: "134", Quantity: 1, Seller: "1"},
	}, "01310100")
	if err != nil {
		t.Fatal(err)
	}

	if gotBody.PostalCode != "01310100" || gotBody.Country != "BRA" {
		t.Errorf("request = %+v", gotBody)
	}
	if len(gotBody.Items) != 1 || gotBody.Items[0].Seller != "1" {
		t.Errorf("items = %+v", gotBody.Items)
	}

	if len(sim.LogisticsInfo) == 0 || len(sim.LogisticsInfo[0].SLAs) == 0 {
		t.Fatal("no SLAs parsed from the live fixture")
	}
	sla := sim.LogisticsInfo[0].SLAs[0]
	// SimulationSLA carries Name; the orderForm's SLA struct uses ID.
	if sla.Name != "Entrega Agendada" {
		t.Errorf("SLA = %q, want Entrega Agendada", sla.Name)
	}
	// Recorded live on 2026-08-07.
	if len(sla.AvailableDeliveryWindows) != 66 {
		t.Errorf("windows = %d, want 66", len(sla.AvailableDeliveryWindows))
	}
	if sla.Price != 0 {
		t.Errorf("shipping = %d, want 0 (free delivery)", int64(sla.Price))
	}
}

func TestSimulateWindowsAreIndexedFromZero(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(mustReadFile(t, "testdata/simulation.json"))
	})
	sim, err := c.Simulate([]vtex.SimulationItemRequest{{ID: "134", Quantity: 1, Seller: "1"}}, "01310100")
	if err != nil {
		t.Fatal(err)
	}
	// `--window N` indexes into this slice, so the indices must match.
	for i, dw := range sim.LogisticsInfo[0].SLAs[0].AvailableDeliveryWindows {
		if dw.Index != i {
			t.Fatalf("window %d has Index %d", i, dw.Index)
		}
		if dw.RawStart == "" || dw.RawEnd == "" {
			t.Fatalf("window %d lost its raw timestamps; the shipping request must echo them verbatim", i)
		}
	}
}

func TestSimulateExposesPaymentSystems(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"logisticsInfo":[],
			"paymentData":{"paymentSystems":[
				{"id":125,"name":"Pix","groupName":"instantPaymentPaymentGroup"}]}}`))
	})
	sim, err := c.Simulate(nil, "01310100")
	if err != nil {
		t.Fatal(err)
	}
	// This is how payment methods are discoverable before login.
	if len(sim.PaymentSystems) != 1 || sim.PaymentSystems[0].ID != 125 {
		t.Errorf("payment systems = %+v", sim.PaymentSystems)
	}
}

func TestSimulateFallsBackToSLAIDWhenNameMissing(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// zonasul's orderForm SLAs carry id but no name.
		_, _ = w.Write([]byte(`{"items":[],"logisticsInfo":[
			{"slas":[{"id":"AGENDADA","price":700,"availableDeliveryWindows":[]}]}]}`))
	})
	sim, err := c.Simulate(nil, "01310100")
	if err != nil {
		t.Fatal(err)
	}
	if got := sim.LogisticsInfo[0].SLAs[0].Name; got != "AGENDADA" {
		t.Errorf("Name = %q, want the id as fallback", got)
	}
}

func TestGetDeliveryWindowsDeduplicatesAcrossItems(t *testing.T) {
	// Two items offering identical windows must produce one list. Without
	// dedup, `--window 3` would point at a duplicate of window 1.
	body := `{"orderFormId":"OF","shippingData":{"logisticsInfo":[
		{"slas":[{"id":"A","availableDeliveryWindows":[
			{"startDateUtc":"2026-08-08T14:00:00+00:00","endDateUtc":"2026-08-08T16:00:00+00:00","price":700},
			{"startDateUtc":"2026-08-08T16:00:00+00:00","endDateUtc":"2026-08-08T18:00:00+00:00","price":0}]}]},
		{"slas":[{"id":"A","availableDeliveryWindows":[
			{"startDateUtc":"2026-08-08T14:00:00+00:00","endDateUtc":"2026-08-08T16:00:00+00:00","price":700}]}]}]}}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	got, err := c.GetDeliveryWindows("OF")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d windows, want 2 after dedup", len(got))
	}
	for i, dw := range got {
		if dw.Index != i {
			t.Errorf("window %d has Index %d; indices must be contiguous from zero", i, dw.Index)
		}
	}
	if got[0].Price != money.Centavos(700) || got[1].Price != 0 {
		t.Errorf("prices = %d, %d", int64(got[0].Price), int64(got[1].Price))
	}
	if got[0].Start.Hour() != 14 {
		t.Errorf("start hour = %d, want 14", got[0].Start.Hour())
	}
}

func TestGetDeliveryWindowsEmptyWhenNoAddress(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderFormId":"OF","shippingData":{"logisticsInfo":[]}}`))
	})
	got, err := c.GetDeliveryWindows("OF")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d windows, want none", len(got))
	}
}
