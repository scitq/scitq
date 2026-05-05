package server

import (
	"context"
	"errors"
	"strings"

	"github.com/scitq/scitq/server/providers"
)

// Job error classes surfaced on the job queue and used by the UI
// banner / CLI. See migrations/000028_job_error_class.up.sql for the
// schema and 2026-05-05 incident for the motivating story.
const (
	ErrClassAuth              = "auth"
	ErrClassQuota             = "quota"
	ErrClassCapacity          = "capacity"
	ErrClassUnsupportedFlavor = "unsupported_flavor"
	ErrClassTransient         = "transient"
	ErrClassUnknown           = "unknown"
)

// classifyProviderError maps a provider error to a stable category
// string suitable for the job.error_class column. Pattern-matching is
// intentionally generous (we'd rather over-match into a known class
// than over-default to "unknown") because the *whole point* is to
// give the operator a one-glance signal that doesn't require reading
// the raw message. The raw message is kept too (job.error_message)
// for the cases that fall through to "unknown".
//
// Recognized signals (current set; extend as new failure modes show up):
//
//	auth                — credentials invalid (Azure ClientSecretCredential
//	                      authentication failed; OVH 401; OpenStack
//	                      "Authentication required"). Non-retryable.
//	quota               — subscription / region vCPU caps exceeded.
//	capacity            — region/zone stockout for the SKU.
//	unsupported_flavor  — typed via providers.ErrUnsupportedFlavor.
//	                      Already non-retryable today.
//	transient           — timeouts, network resets, 5xx — retry is sane.
//	unknown             — fall-through; raw message is the diagnostic.
func classifyProviderError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, providers.ErrUnsupportedFlavor) {
		return ErrClassUnsupportedFlavor
	}
	if errors.Is(err, providers.ErrInstanceLimitReached) {
		return ErrClassQuota
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrClassTransient
	}

	msg := strings.ToLower(err.Error())

	// Auth — provider-specific phrasings; Azure SDK puts the credential
	// type and the literal "authentication failed" in its error string.
	switch {
	case strings.Contains(msg, "clientsecretcredential authentication failed"),
		strings.Contains(msg, "authentication required"),
		strings.Contains(msg, "invalid_client"),
		strings.Contains(msg, "invalidauthenticationtoken"),
		strings.Contains(msg, "expired"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "401 "), strings.HasSuffix(msg, "401"),
		strings.Contains(msg, "403 "), strings.HasSuffix(msg, "403"):
		return ErrClassAuth
	}

	// Quota — Azure tags it `QuotaExceeded` or `OperationNotAllowed`,
	// OVH/OpenStack as `quota` in plain English.
	if strings.Contains(msg, "quotaexceeded") ||
		strings.Contains(msg, "operationnotallowed") ||
		strings.Contains(msg, "quota") {
		return ErrClassQuota
	}

	// Capacity / stockout. Distinct from quota: the SKU is allowed but
	// the region has none free right now. Operator's only fix is wait
	// or pick a different region/SKU.
	if strings.Contains(msg, "allocationfailed") ||
		strings.Contains(msg, "zonalallocationfailed") ||
		strings.Contains(msg, "skunotavailable") ||
		strings.Contains(msg, "out of stock") ||
		strings.Contains(msg, "no available capacity") {
		return ErrClassCapacity
	}

	// Transient — network / timeout / generic 5xx. Retry is sane.
	if strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "503 ") || strings.HasSuffix(msg, "503") ||
		strings.Contains(msg, "500 ") || strings.HasSuffix(msg, "500") ||
		strings.Contains(msg, "502 ") || strings.HasSuffix(msg, "502") ||
		strings.Contains(msg, "504 ") || strings.HasSuffix(msg, "504") {
		return ErrClassTransient
	}

	return ErrClassUnknown
}

// isNonRetryable returns true for error classes that won't be fixed by
// retrying (so processJobWithTimeout marks the job F immediately
// instead of burning the retry budget).
func isNonRetryable(class string) bool {
	switch class {
	case ErrClassAuth, ErrClassUnsupportedFlavor:
		return true
	default:
		return false
	}
}
