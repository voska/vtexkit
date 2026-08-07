package errfmt_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/voska/vtexkit/cli/errfmt"
)

func TestDomainCarriesCode9(t *testing.T) {
	err := errfmt.Domain("minimum order R$100,00, current total R$42,00")
	var e *errfmt.Error
	if !errors.As(err, &e) {
		t.Fatal("Domain() must return *Error")
	}
	// zonasul v0.5.0 published 9 as a stable exit code. The meaning
	// generalizes from "min order" to any store business rule, but the
	// number cannot move without breaking published behavior.
	if e.Code != 9 {
		t.Errorf("code = %d, want 9", e.Code)
	}
}

func TestWrapPreservesDetail(t *testing.T) {
	err := errfmt.Wrap(errfmt.ExitAuth, "auto-refresh failed", fmt.Errorf("network down"))
	if err.Error() != "auto-refresh failed: network down" {
		t.Errorf("Error() = %q", err.Error())
	}
	if err.Code != errfmt.ExitAuth {
		t.Errorf("code = %d, want %d", err.Code, errfmt.ExitAuth)
	}
}

func TestErrorWithoutDetailOmitsSeparator(t *testing.T) {
	if got := errfmt.Auth("not logged in").Error(); got != "not logged in" {
		t.Errorf("Error() = %q, want %q", got, "not logged in")
	}
}

func TestExitCodesMatchPublishedValues(t *testing.T) {
	// These are the exit codes zonasul v0.5.0 documents. Changing any of
	// them silently breaks every agent and script that branches on them.
	want := map[string]int{
		"ok": errfmt.ExitOK, "error": errfmt.ExitError, "usage": errfmt.ExitUsage,
		"empty": errfmt.ExitEmpty, "auth": errfmt.ExitAuth, "notfound": errfmt.ExitNotFound,
		"forbidden": errfmt.ExitForbidden, "ratelimit": errfmt.ExitRateLimit,
		"retryable": errfmt.ExitRetryable, "domain": errfmt.ExitDomain,
		"config": errfmt.ExitConfig,
	}
	expected := map[string]int{
		"ok": 0, "error": 1, "usage": 2, "empty": 3, "auth": 4, "notfound": 5,
		"forbidden": 6, "ratelimit": 7, "retryable": 8, "domain": 9, "config": 10,
	}
	for name, got := range want {
		if got != expected[name] {
			t.Errorf("%s = %d, want %d", name, got, expected[name])
		}
	}
}

func TestExitCodeTableIsIndexAligned(t *testing.T) {
	table := errfmt.ExitCodeTable()
	if len(table) != 11 {
		t.Fatalf("table has %d entries, want 11", len(table))
	}
	for i, e := range table {
		if e.Code != i {
			t.Errorf("entry %d has code %d; table must be index-aligned 0..10", i, e.Code)
		}
		if e.Name == "" || e.Description == "" {
			t.Errorf("code %d has an empty name or description", e.Code)
		}
	}
}

func TestOnlyRateLimitAndRetryableAreTransient(t *testing.T) {
	// Agents branch on this: transient means retry with backoff, permanent
	// means fail fast. Getting it wrong makes agents either give up on
	// blips or hammer permanent failures.
	transient := map[int]bool{errfmt.ExitRateLimit: true, errfmt.ExitRetryable: true}
	for _, e := range errfmt.ExitCodeTable() {
		if e.Transient != transient[e.Code] {
			t.Errorf("code %d (%s) transient = %v, want %v", e.Code, e.Name, e.Transient, transient[e.Code])
		}
	}
}
