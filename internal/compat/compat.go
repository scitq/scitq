// Package compat checks scitq's recorded public surfaces (compat/*.txt)
// against the code. See docs/reference/compatibility.md.
package compat

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// CheckRecorded requires the recorded surface file to match the current
// surface exactly, so every change shows up as a reviewed diff. With update,
// it rewrites the file instead.
func CheckRecorded(t *testing.T, path string, current []string, update bool) {
	t.Helper()
	if update {
		if err := os.WriteFile(path, []byte(strings.Join(current, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s (generate it with -update-surface): %v", path, err)
	}
	recorded := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		recorded[l] = true
	}
	now := map[string]bool{}
	var added, removed []string
	for _, l := range current {
		now[l] = true
		if !recorded[l] {
			added = append(added, l)
		}
	}
	for l := range recorded {
		if !now[l] {
			removed = append(removed, l)
		}
	}
	sort.Strings(removed)
	if len(added)+len(removed) > 0 {
		t.Fatalf("%s is out of date.\nAdded:\n  %s\nRemoved:\n  %s\n"+
			"Removals break users of the previous release (docs/reference/compatibility.md). "+
			"For deliberate changes, rerun with -update-surface and commit the file.",
			path, strings.Join(added, "\n  "), strings.Join(removed, "\n  "))
	}
}
