// Package errfmt defines the stable exit codes and typed errors shared by
// every store CLI built on vtexkit.
//
// The codes are a published contract: agents and scripts branch on them, so
// a number may never change meaning across releases. Codes 7 and 8 are the
// only transient ones — everything else means fail fast.
package errfmt

import "fmt"

const (
	ExitOK        = 0
	ExitError     = 1
	ExitUsage     = 2
	ExitEmpty     = 3
	ExitAuth      = 4
	ExitNotFound  = 5
	ExitForbidden = 6
	ExitRateLimit = 7
	ExitRetryable = 8
	// ExitDomain reports that a store business rule refused the operation.
	// zonasul v0.5.0 published this code as "cart below R$100 minimum";
	// the number is preserved but the meaning is generalized, because a
	// minimum order is meaningless for a store that has none.
	ExitDomain = 9
	ExitConfig = 10
)

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Detail)
	}
	return e.Message
}

func New(code int, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

func Wrap(code int, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Detail: err.Error()}
}

func Auth(msg string) *Error      { return New(ExitAuth, msg) }
func NotFound(msg string) *Error  { return New(ExitNotFound, msg) }
func Usage(msg string) *Error     { return New(ExitUsage, msg) }
func Empty() *Error               { return New(ExitEmpty, "no results") }
func Forbidden(msg string) *Error { return New(ExitForbidden, msg) }
func RateLimit() *Error           { return New(ExitRateLimit, "rate limited — retry later") }
func Retryable(msg string) *Error { return New(ExitRetryable, msg) }
func Config(msg string) *Error    { return New(ExitConfig, msg) }

// Domain reports a store business rule refusing the operation: an order
// below the store minimum, an unavailable item, a closed store. The message
// carries the specific rule so a caller knows what happened; the code stays
// stable so an agent can branch on it.
func Domain(msg string) *Error { return New(ExitDomain, msg) }

type ExitCodeEntry struct {
	Code        int    `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Transient   bool   `json:"transient"`
}

// ExitCodeTable is the machine-readable exit code reference, exposed by
// every CLI as `<store> exit-codes --json`. Entries are index-aligned to
// their codes.
func ExitCodeTable() []ExitCodeEntry {
	return []ExitCodeEntry{
		{ExitOK, "success", "Operation completed successfully", false},
		{ExitError, "error", "General error", false},
		{ExitUsage, "usage", "Invalid usage or arguments", false},
		{ExitEmpty, "empty", "No results found", false},
		{ExitAuth, "auth_required", "Authentication required or token expired", false},
		{ExitNotFound, "not_found", "Resource not found", false},
		{ExitForbidden, "forbidden", "Permission denied", false},
		{ExitRateLimit, "rate_limited", "API rate limit exceeded", true},
		{ExitRetryable, "retryable", "Transient error, safe to retry", true},
		{ExitDomain, "domain_error", "Store business rule refused the operation", false},
		{ExitConfig, "config_error", "Configuration error", false},
	}
}
