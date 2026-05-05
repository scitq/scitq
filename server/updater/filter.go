package updater

import (
	"fmt"
	"log"
	"regexp"
)

// FlavorFilter applies operator-configured include/exclude regex rules
// to flavor names at sync time. See
// memory/project_recruitment_followups.md for the design rationale.
//
// Logic:
//   - If `Includes` is empty: every name passes the include gate.
//   - Otherwise: a name must match at least one Includes pattern.
//   - If `Excludes` is empty: nothing is excluded.
//   - Otherwise: a name matching ANY Excludes pattern is rejected.
//   - Net: a name is allowed iff include-passes AND exclude-doesn't-match.
type FlavorFilter struct {
	Includes []*regexp.Regexp
	Excludes []*regexp.Regexp
}

// CompileFlavorFilter builds a FlavorFilter from the YAML pattern lists.
// Compilation errors are returned wrapped with which pattern failed and
// in which list, so the operator can spot a typo without grepping logs.
func CompileFlavorFilter(includes, excludes []string) (*FlavorFilter, error) {
	f := &FlavorFilter{}
	for i, p := range includes {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("flavor_include_patterns[%d] (%q): %w", i, p, err)
		}
		f.Includes = append(f.Includes, re)
	}
	for i, p := range excludes {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("flavor_exclude_patterns[%d] (%q): %w", i, p, err)
		}
		f.Excludes = append(f.Excludes, re)
	}
	if len(f.Includes) > 0 || len(f.Excludes) > 0 {
		log.Printf("FlavorFilter: %d include / %d exclude pattern(s) compiled", len(f.Includes), len(f.Excludes))
	}
	return f, nil
}

// Allows returns true if the flavor name passes the include/exclude
// gates. A nil filter allows everything (so callers can pass nil
// without a guard).
func (f *FlavorFilter) Allows(name string) bool {
	if f == nil {
		return true
	}
	if len(f.Includes) > 0 {
		matched := false
		for _, re := range f.Includes {
			if re.MatchString(name) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, re := range f.Excludes {
		if re.MatchString(name) {
			return false
		}
	}
	return true
}
