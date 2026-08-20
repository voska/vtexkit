package vtex_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voska/vtexkit/money"
	"github.com/voska/vtexkit/store"
	"github.com/voska/vtexkit/vtex"
)

// subscriptionJSON mirrors the shape grupomantiqueira really returns, trimmed
// to the fields this package reads, with every identifier and the email
// replaced by synthetic values — a subscription ID is account data, and this
// repo's fixtures carry public product data only.
//
// The price encoding is the point: 6590.0 is R$65,90 in centavos, matching a
// shelf price of R$64,90 — not 6590 reais.
const subscriptionJSON = `{
  "id":"7A1C4E9B2F6D48A3B5C7E1F09D2B6A84",
  "customerEmail":"shopper@example.com",
  "title":null,
  "status":"ACTIVE",
  "isSkipped":false,
  "nextPurchaseDate":"2026-09-03T09:00:00Z",
  "lastPurchaseDate":"2026-08-20T00:00:00Z",
  "plan":{"id":"vtex.subscription.assinatura07d",
          "frequency":{"periodicity":"DAILY","interval":14}},
  "purchaseSettings":{"selectedSla":"Receba às terças-feiras",
                      "paymentMethod":{"paymentSystemName":"creditCard"}},
  "cycleCount":25,
  "items":[{"id":"C3E5A7091B2D4F68A0C2E4B6D8F1A395","skuId":"1","quantity":1,
            "isSkipped":false,"status":"ACTIVE","priceAtSubscriptionDate":6590.0}]
}`

// newSubsClient points the Subscriptions host at a test server while leaving
// BaseURL pointing somewhere else, so a handler that fires proves the call
// went to the account host rather than the storefront domain.
func newSubsClient(t *testing.T, h http.HandlerFunc) *vtex.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := vtex.New(store.Store{
		Name: "test", Account: "testacct",
		BaseURL: "https://storefront.invalid",
	}, "TOKEN")
	c.SubscriptionsURL = srv.URL
	return c
}

func TestSubscriptionsUseAccountHostNotStorefront(t *testing.T) {
	// RNS derives the account from the request subdomain, so a www vanity
	// domain resolves account "www" and 400s. Guard the routing.
	s := store.Store{Name: "mantiqueira", Account: "grupomantiqueira",
		BaseURL: "https://www.mantiqueiraemcasa.com.br"}
	if got, want := s.AccountBaseURL(),
		"https://grupomantiqueira.vtexcommercestable.com.br"; got != want {
		t.Errorf("AccountBaseURL() = %q, want %q", got, want)
	}
}

func TestListSubscriptionsParsesBareArray(t *testing.T) {
	var gotPath string
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// Unlike the OMS order list, RNS returns a bare array.
		_, _ = w.Write([]byte("[" + subscriptionJSON + "]"))
	})

	subs, err := c.ListSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/rns/pub/subscriptions" {
		t.Errorf("path = %s", gotPath)
	}
	if len(subs) != 1 {
		t.Fatalf("got %d subscriptions", len(subs))
	}
	got := subs[0]
	if got.ID != "7A1C4E9B2F6D48A3B5C7E1F09D2B6A84" || got.Status != vtex.SubscriptionActive {
		t.Errorf("subscription = %+v", got)
	}
	if got.Settings.DeliveryWindow != "Receba às terças-feiras" {
		t.Errorf("DeliveryWindow = %q", got.Settings.DeliveryWindow)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %d items", len(got.Items))
	}
	// The whole reason this field is not a float: 6590.0 means R$65,90.
	if got.Items[0].Price != money.Centavos(6590) {
		t.Errorf("Price = %d centavos (%s), want 6590",
			int64(got.Items[0].Price), got.Items[0].Price)
	}
}

func TestListSubscriptionsPagesUntilShortPage(t *testing.T) {
	// RNS caps the page size at 15, so a shopper with more than that would
	// silently lose the rest of the list on a single-request implementation.
	var gotPages []string
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		gotPages = append(gotPages, page)
		n := 15 // full page
		if page == "2" {
			n = 3 // short page ends the walk
		}
		items := make([]string, n)
		for i := range items {
			items[i] = subscriptionJSON
		}
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	})
	subs, err := c.ListSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 18 {
		t.Errorf("got %d subscriptions, want 18 across two pages", len(subs))
	}
	if len(gotPages) != 2 || gotPages[0] != "1" || gotPages[1] != "2" {
		t.Errorf("pages requested = %v, want [1 2]", gotPages)
	}
}

func TestListSubscriptionsRefusesToTruncate(t *testing.T) {
	// A server that never returns a short page must produce an error, not a
	// quietly capped list that downstream cannot distinguish from complete.
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		items := make([]string, 15)
		for i := range items {
			items[i] = subscriptionJSON
		}
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	})
	if _, err := c.ListSubscriptions(); err == nil {
		t.Fatal("an unbounded list must fail rather than truncate")
	}
}

func TestListSubscriptionsSendsAuthCookie(t *testing.T) {
	var gotCookie string
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie("VtexIdclientAutCookie_testacct"); err == nil {
			gotCookie = ck.Value
		}
		_, _ = w.Write([]byte("[]"))
	})
	if _, err := c.ListSubscriptions(); err != nil {
		t.Fatal(err)
	}
	// RNS accepts the shopper token; without it the call is unauthorized.
	if gotCookie != "TOKEN" {
		t.Errorf("auth cookie = %q, want TOKEN", gotCookie)
	}
}

func TestListSubscriptionsEmpty(t *testing.T) {
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	subs, err := c.ListSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	// A shopper with no subscriptions is exit code 3, not an error.
	if len(subs) != 0 {
		t.Errorf("got %d subscriptions", len(subs))
	}
}

func TestGetSubscriptionByID(t *testing.T) {
	var gotPath string
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(subscriptionJSON))
	})
	got, err := c.GetSubscription("7A1C4E9B2F6D48A3B5C7E1F09D2B6A84")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/rns/pub/subscriptions/7A1C4E9B2F6D48A3B5C7E1F09D2B6A84" {
		t.Errorf("path = %s", gotPath)
	}
	if got.CycleCount != 25 || got.NextPurchaseDate != "2026-09-03T09:00:00Z" {
		t.Errorf("subscription = %+v", got)
	}
}

func TestSetSubscriptionStatusPatches(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(strings.Replace(subscriptionJSON,
			`"status":"ACTIVE"`, `"status":"PAUSED"`, 1)))
	})
	got, err := c.SetSubscriptionStatus("ABC", vtex.SubscriptionPaused)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", gotMethod)
	}
	if gotBody["status"] != "PAUSED" || len(gotBody) != 1 {
		t.Errorf("body = %v, want only {status: PAUSED}", gotBody)
	}
	if got.Status != vtex.SubscriptionPaused {
		t.Errorf("Status = %q", got.Status)
	}
}

func TestSetSubscriptionStatusRefusesCancel(t *testing.T) {
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("cancel must never reach the network")
	})
	// CANCELED is terminal in RNS, so the CLI must not be able to reach it.
	if _, err := c.SetSubscriptionStatus("ABC", vtex.SubscriptionCanceled); err == nil {
		t.Fatal("cancelling must be refused")
	}
	if _, err := c.SetSubscriptionStatus("ABC", "NONSENSE"); err == nil {
		t.Fatal("unknown status must be refused")
	}
}

func TestPatchNotModifiedReadsBackState(t *testing.T) {
	// Resuming an already-active subscription answers 304 with an empty
	// body. Treating that as a parse failure made `subs resume` report an
	// error for a request that had in fact succeeded.
	var calls []string
	c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(subscriptionJSON))
	})
	got, err := c.SetSubscriptionStatus("ABC", vtex.SubscriptionActive)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != vtex.SubscriptionActive {
		t.Errorf("Status = %q", got.Status)
	}
	if len(calls) != 2 || calls[1] != http.MethodGet {
		t.Errorf("calls = %v, want PATCH then GET", calls)
	}
}

func TestSkipSubscriptionTogglesIsSkipped(t *testing.T) {
	for _, skip := range []bool{true, false} {
		var gotBody map[string]any
		c := newSubsClient(t, func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotBody)
			_, _ = w.Write([]byte(subscriptionJSON))
		})
		if _, err := c.SkipSubscription("ABC", skip); err != nil {
			t.Fatal(err)
		}
		if gotBody["isSkipped"] != skip || len(gotBody) != 1 {
			t.Errorf("body = %v, want only {isSkipped: %v}", gotBody, skip)
		}
	}
}

func TestFrequencyReadsLikeAShopperWouldSay(t *testing.T) {
	tests := []struct {
		in   vtex.Frequency
		want string
	}{
		// Mantiqueira's real plan: DAILY with interval 14.
		{vtex.Frequency{Periodicity: "DAILY", Interval: 14}, "every 14 days"},
		{vtex.Frequency{Periodicity: "WEEKLY", Interval: 1}, "every week"},
		{vtex.Frequency{Periodicity: "MONTHLY", Interval: 3}, "every 3 months"},
		{vtex.Frequency{Periodicity: "YEARLY", Interval: 0}, "every year"},
		{vtex.Frequency{Periodicity: "FORTNIGHTLY"}, "FORTNIGHTLY"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("Frequency%+v.String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}
