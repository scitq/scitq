package fetch

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestClassifyFetchError_HardSignals ensures that unambiguous
// permanent-failure error strings are tagged with errPermanent so
// the retry loop bails out immediately instead of burning its budget.
func TestClassifyFetchError_HardSignals(t *testing.T) {
	cases := []string{
		"HTTP response: 404 Not Found",
		"GET https://ebi.ac.uk/x returned 403",
		"401 Unauthorized",
		"malformed URL",
		"invalid url ftp://",
	}
	for _, s := range cases {
		err := classifyFetchError(fmt.Errorf("%s", s))
		if !errors.Is(err, errPermanent) {
			t.Errorf("expected permanent for %q, got %v", s, err)
		}
	}
}

// TestClassifyFetchError_RetryablePassThrough: transient failures
// must stay retryable (not errPermanent) so the next attempt gets
// a shot.
func TestClassifyFetchError_RetryablePassThrough(t *testing.T) {
	cases := []string{
		"connection reset by peer",
		"i/o timeout",
		"HTTP response: 503 Service Unavailable",
		"EOF",
		"md5 mismatch for ERR123.fastq.gz",
		"temporary failure in name resolution",
	}
	for _, s := range cases {
		err := classifyFetchError(fmt.Errorf("%s", s))
		if errors.Is(err, errPermanent) {
			t.Errorf("%q should be retryable, got permanent: %v", s, err)
		}
	}
}

// TestClassifyFetchError_NilIsNil: a nil error must stay nil (the
// retry loop uses the return value to decide success).
func TestClassifyFetchError_NilIsNil(t *testing.T) {
	if classifyFetchError(nil) != nil {
		t.Fatal("classifyFetchError(nil) must return nil")
	}
}

// TestRetryFetchURL_StopsOnPermanent: a 404 fails exactly once, not
// three times. Backoffs would be 10s + 30s wasted otherwise.
func TestRetryFetchURL_StopsOnPermanent(t *testing.T) {
	calls := 0
	err := retryFetchURL("test", "http://nowhere/x", func() error {
		calls++
		return fmt.Errorf("HTTP response: 404 Not Found")
	})
	if calls != 1 {
		t.Fatalf("permanent error should run attempt exactly once, got %d", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected the 404 error to surface, got %v", err)
	}
}

// TestRetryFetchURL_SucceedsOnSecondAttempt: a transient hiccup
// cleared by the next attempt yields nil error and only two calls.
// Can't easily test the actual sleep without faking time; we trust
// time.Sleep works and verify the control flow.
func TestRetryFetchURL_SucceedsOnSecondAttempt(t *testing.T) {
	// Shortcut backoffs for test speed — temporarily replace with 0.
	// (We can't modify the consts, but we can accept the 10s wait
	// since the test asserts the retry happened.)
	// Skip the actual wait by running attempt() that succeeds on 2.
	// Even with 10s backoff, go test's default timeout is 10 min.
	calls := 0
	err := retryFetchURL("test", "http://flaky/x", func() error {
		calls++
		if calls == 1 {
			return fmt.Errorf("connection reset by peer")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success on attempt 2, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls (fail then success), got %d", calls)
	}
}

// TestCircuitBreaker_DemotesAfterThreshold: three consecutive
// failures on a channel push it to the end of a passed options list.
// First success resets the demotion.
func TestCircuitBreaker_DemotesAfterThreshold(t *testing.T) {
	resetBreakerForTest(t)

	// Below threshold: still in original order.
	recordChannelFailure("ena-ftp")
	recordChannelFailure("ena-ftp")
	got := reorderOptionsForBreaker([]string{"ena-ftp", "sra-aws", "sra-tools"})
	if got[0] != "ena-ftp" {
		t.Fatalf("below threshold should keep ena-ftp first, got %v", got)
	}

	// Hitting the threshold demotes.
	recordChannelFailure("ena-ftp")
	got = reorderOptionsForBreaker([]string{"ena-ftp", "sra-aws", "sra-tools"})
	if got[0] == "ena-ftp" {
		t.Fatalf("after %d failures ena-ftp must be demoted, got %v", channelDemoteThreshold, got)
	}
	// Demoted channel still appears, but at the end.
	if got[len(got)-1] != "ena-ftp" {
		t.Fatalf("demoted channel should be last, got %v", got)
	}

	// A success restores it.
	recordChannelSuccess("ena-ftp")
	got = reorderOptionsForBreaker([]string{"ena-ftp", "sra-aws", "sra-tools"})
	if got[0] != "ena-ftp" {
		t.Fatalf("after success ena-ftp should be first again, got %v", got)
	}
}

// TestCircuitBreaker_MultipleChannelsIndependent: demoting one
// channel does NOT demote the others.
func TestCircuitBreaker_MultipleChannelsIndependent(t *testing.T) {
	resetBreakerForTest(t)

	for i := 0; i < channelDemoteThreshold; i++ {
		recordChannelFailure("sra-aws")
	}
	got := reorderOptionsForBreaker([]string{"ena-ftp", "sra-aws", "sra-tools"})
	// sra-aws should be last; ena-ftp and sra-tools stay in their
	// original relative order at the front.
	if got[len(got)-1] != "sra-aws" {
		t.Fatalf("sra-aws should be demoted last, got %v", got)
	}
	if got[0] != "ena-ftp" || got[1] != "sra-tools" {
		t.Fatalf("healthy channels should keep order, got %v", got)
	}
}

// resetBreakerForTest clears the package-level circuit breaker state
// between tests. Tests must be serial (go test's default) because
// the breaker is package-global by design — same as the production
// per-worker scope.
func resetBreakerForTest(t *testing.T) {
	t.Helper()
	channelBreaker.mu.Lock()
	for k := range channelBreaker.failures {
		delete(channelBreaker.failures, k)
	}
	for k := range channelBreaker.demoted {
		delete(channelBreaker.demoted, k)
	}
	for k := range channelBreaker.warned {
		delete(channelBreaker.warned, k)
	}
	channelBreaker.mu.Unlock()
}
