package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/scitq/scitq/server/providers"
)

func TestClassifyProviderError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		// Typed sentinels — the hardest signal, take precedence over
		// pattern matching.
		{"unsupported_flavor sentinel", providers.ErrUnsupportedFlavor, ErrClassUnsupportedFlavor},
		{"unsupported_flavor wrapped", fmt.Errorf("create: %w", providers.ErrUnsupportedFlavor), ErrClassUnsupportedFlavor},
		{"instance_limit sentinel", providers.ErrInstanceLimitReached, ErrClassQuota},
		{"context deadline", context.DeadlineExceeded, ErrClassTransient},
		{"context canceled", context.Canceled, ErrClassTransient},

		// The smoking gun from 2026-05-05.
		{"azure auth verbatim", errors.New("ClientSecretCredential authentication failed"), ErrClassAuth},
		{"azure auth wrapped", fmt.Errorf("delete VM after retries: after 3 attempts, last error: failed to begin VM deletion: ClientSecretCredential authentication failed"), ErrClassAuth},
		{"openstack auth", errors.New("Authentication required"), ErrClassAuth},
		{"oauth invalid_client", errors.New("oauth: invalid_client"), ErrClassAuth},
		{"401 status", errors.New("HTTP 401 Unauthorized"), ErrClassAuth},
		{"InvalidAuthenticationToken", errors.New("InvalidAuthenticationToken: token is expired"), ErrClassAuth},

		// Quota.
		{"azure QuotaExceeded", errors.New("Operation results in exceeding quota limits of Resource Type: Cores. Maximum allowed: 10"), ErrClassQuota},
		{"azure OperationNotAllowed", errors.New("Code: OperationNotAllowed"), ErrClassQuota},
		{"plain 'quota' word", errors.New("over quota"), ErrClassQuota},

		// Capacity / stockout.
		{"azure AllocationFailed", errors.New("AllocationFailed: capacity"), ErrClassCapacity},
		{"azure ZonalAllocationFailed", errors.New("ZonalAllocationFailed in zone 1"), ErrClassCapacity},
		{"azure SkuNotAvailable", errors.New("SkuNotAvailable in this region"), ErrClassCapacity},
		{"openstack out of stock", errors.New("Out of stock"), ErrClassCapacity},

		// Transient.
		{"i/o timeout", errors.New("dial tcp: i/o timeout"), ErrClassTransient},
		{"connection reset", errors.New("read: connection reset by peer"), ErrClassTransient},
		{"503", errors.New("HTTP 503 Service Unavailable"), ErrClassTransient},
		{"504 suffix", errors.New("got status 504"), ErrClassTransient},

		// Fall-through.
		{"random message", errors.New("something weird happened on the cloud side"), ErrClassUnknown},
		{"empty", errors.New(""), ErrClassUnknown},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyProviderError(c.err)
			if got != c.want {
				t.Errorf("classify(%q) = %q, want %q", c.err.Error(), got, c.want)
			}
		})
	}

	// nil input must return empty (used by call sites that store empty
	// in error_class for non-failed jobs).
	if got := classifyProviderError(nil); got != "" {
		t.Errorf("classify(nil) = %q, want empty", got)
	}
}

func TestIsNonRetryable(t *testing.T) {
	cases := []struct {
		class string
		want  bool
	}{
		{ErrClassAuth, true},
		{ErrClassUnsupportedFlavor, true},
		{ErrClassQuota, false},
		{ErrClassCapacity, false},
		{ErrClassTransient, false},
		{ErrClassUnknown, false},
		{"", false},
	}
	for _, c := range cases {
		if got := isNonRetryable(c.class); got != c.want {
			t.Errorf("isNonRetryable(%q) = %v, want %v", c.class, got, c.want)
		}
	}
}
