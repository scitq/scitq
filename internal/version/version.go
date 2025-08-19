package version

import "runtime/debug"

// Set via -ldflags; fallbacks if VCS stamping is unavailable.
var (
	Version = "dev"     // e.g. v0.1.0 or git describe
	Commit  = "none"    // short sha
	Date    = "unknown" // build time, UTC
)

type VCSInfo struct {
	System   string // "git"
	Revision string // full sha
	Time     string // RFC3339 commit time
	Modified string // "true"/"false"
}

func ReadVCS() (v VCSInfo, ok bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return VCSInfo{}, false
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs":
			v.System = s.Value
		case "vcs.revision":
			v.Revision = s.Value
		case "vcs.time":
			v.Time = s.Value
		case "vcs.modified":
			v.Modified = s.Value
		}
	}
	return v, v.Revision != ""
}

func Full() string {
	if vcs, ok := ReadVCS(); ok && vcs.Revision != "" {
		return Version + " (" + vcs.System + " " + short(vcs.Revision) + ", " + vcs.Time + ", dirty=" + vcs.Modified + ")"
	}
	return Version + " (" + Commit + ", " + Date + ")"
}

func short(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}
