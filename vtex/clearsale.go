package vtex

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"

	"github.com/voska/vtexkit/store"
)

const (
	clearSaleAppKey  = "b5qhnn79ksdoeru452lt"
	clearSaleBaseURL = "https://device.clearsale.com.br/p"
	clearSaleSDK     = "@clear.sale/behavior-analytics-fingerprint-sdk"
	clearSaleVersion = "1.0.253"
)

// GenerateClearSaleSession registers an anti-fraud device fingerprint and
// returns the session ID to send as deviceFingerprint on a card payment.
//
// Nothing here is store-specific — the app key and SDK identifiers are
// common to the VTEX/ClearSale integration — so it lives in the library and
// is reached only when a store sets the ClearSaleFingerprint quirk.
//
// doer and endpoint are injected: the original implementation called the
// package-level http.Get, which bypassed the client's transport and had no
// timeout, so a hung ClearSale host stalled checkout indefinitely.
func GenerateClearSaleSession(doer store.HTTPDoer, endpoint string) (string, error) {
	if endpoint == "" {
		endpoint = clearSaleBaseURL
	}
	sid, err := generateSessionID()
	if err != nil {
		return "", err
	}

	// Registration failures are non-fatal: a session ID with no telemetry
	// behind it still lets the payment through, whereas failing here would
	// block checkout on a third party's outage. Callers surface a warning.
	if err := sendFingerprint(doer, endpoint+"/fp1.png", fingerprint1Params(sid)); err != nil {
		return sid, fmt.Errorf("clearsale fp1: %w", err)
	}
	if err := sendFingerprint(doer, endpoint+"/fp2.png", fingerprint2Params(sid)); err != nil {
		return sid, fmt.Errorf("clearsale fp2: %w", err)
	}
	return sid, nil
}

func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("clearsale session id: %w", err)
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func sendFingerprint(doer store.HTTPDoer, endpoint string, params url.Values) error {
	req, err := http.NewRequest(http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := doer.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func fingerprint1Params(sid string) url.Values {
	return url.Values{
		"bb":  {hashString("canvas-cli-" + sid)},
		"ba":  {hashString("webgl-cli-" + sid)},
		"a2":  {hashString("fonts-cli-" + sid)},
		"app": {clearSaleAppKey},
		"sid": {sid},
		"id":  {clearSaleSDK},
		"v":   {clearSaleVersion},
		"sm":  {"true"},
	}
}

func fingerprint2Params(sid string) url.Values {
	return url.Values{
		"aa":  {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"},
		"ab":  {"pt-BR"},
		"ac":  {"30"},
		"ad":  {"2"},
		"ae":  {"1080"},
		"af":  {"1920"},
		"ag":  {"1048"},
		"ah":  {"1920"},
		"ai":  {"180"}, // BRT is UTC-3, expressed in minutes
		"aj":  {"1"},
		"ak":  {"1"},
		"al":  {"1"},
		"am":  {"0"},
		"an":  {"0"},
		"ao":  {"unknown"},
		"ap":  {"MacIntel"},
		"aq":  {"unknown"},
		"ar":  {hashString("canvas-render-" + sid)},
		"as":  {hashString("webgl-render-" + sid)},
		"at":  {"0"},
		"ay":  {hashString("fonts-detect-" + sid)},
		"a3":  {"10"},
		"m1":  {"1"},
		"mb":  {"0"},
		"hd":  {"0"},
		"mr":  {"8"},
		"h1":  {hashString("audio-" + sid)},
		"h6":  {hashString("webgl-ext-" + sid)},
		"h4":  {hashString("webgl-params-" + sid)},
		"l1":  {"0"},
		"im":  {"0"},
		"b2":  {"0.82"},
		"b1":  {"0"},
		"az":  {hashString("misc-" + sid)},
		"h7":  {hashString("webgl-vendor-" + sid)},
		"app": {clearSaleAppKey},
		"sid": {sid},
		"id":  {clearSaleSDK},
		"v":   {clearSaleVersion},
	}
}
