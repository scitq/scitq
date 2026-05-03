package server

// version_compare.go — pure helper for the worker upgrade-status decision.
// Kept in its own file so it can be unit-tested without spinning up a DB.
// See specs/worker_autoupgrade.md.

const (
	UpgradeStatusUpToDate        = "up_to_date"
	UpgradeStatusNeedsUpgrade    = "needs_upgrade"
	UpgradeStatusUnsupportedArch = "unsupported_arch"
	UpgradeStatusUnknown         = "unknown"
)

// SupportedUpgradeArch is the only arch Phase II auto-apply will target.
// Phase I (visibility) reports status for every arch but distinguishes
// `unsupported_arch` from `needs_upgrade` so the UI/CLI can hide the
// "redeploy this one" call-to-action where redeployment is gated on
// multi-arch binary distribution we haven't built yet.
const SupportedUpgradeArch = "linux/amd64"

// WorkerUpgradeStatus computes the derived upgrade status from the worker's
// reported build identity and the server's own commit. Inputs:
//   - workerArch:   GOOS/GOARCH the worker reported, e.g. "linux/amd64".
//                   Empty string = pre-Phase-I worker (didn't report).
//   - workerCommit: the worker's git SHA (full or short — compared as-is).
//                   Empty = pre-Phase-I worker.
//   - serverCommit: the server's own git SHA. If empty (e.g. a `dev` build
//                   that lost its VCS stamp), every worker reports
//                   `unknown` — we can't make a comparison either side.
//
// Returns one of the UpgradeStatus* constants.
func WorkerUpgradeStatus(workerArch, workerCommit, serverCommit string) string {
	// Arch check first — it's a property of the worker alone and is
	// meaningful even when the server didn't get its VCS info stamped
	// (e.g. a `dev` build with no -ldflags). Knowing the arch isn't
	// supported is independent of being able to compare commits.
	if workerArch != "" && workerArch != SupportedUpgradeArch {
		return UpgradeStatusUnsupportedArch
	}
	// Anything missing past this point means we can't make the
	// commit comparison: report unknown.
	if workerArch == "" || workerCommit == "" || serverCommit == "" {
		return UpgradeStatusUnknown
	}
	if workerCommit == serverCommit {
		return UpgradeStatusUpToDate
	}
	return UpgradeStatusNeedsUpgrade
}
