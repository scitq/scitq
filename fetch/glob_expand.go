package fetch

import "strings"

// expandGlobPattern converts a glob pattern that may contain bash
// extglob-style negations (e.g. !(*.tgz), !(*.tgz|*.zip)) into an
// ordered list of rclone filter rules. The returned rules are intended
// to be added to a *filter.Filter before any trailing catch-all "- *",
// in the order returned (rclone matches first-match-wins, so excludes
// must come before the positive include).
//
// Examples:
//
//	"**/*.tgz"            → ["+ **/*.tgz"]
//	"data/!(*.tgz)"       → ["- data/*.tgz", "+ data/*"]
//	"**/!(*.tgz|*.zip)"   → ["- **/*.tgz", "- **/*.zip", "+ **/*"]
//	"a/!(*.tgz)/!(*.bak)" → ["- a/*.tgz/*",
//	                         "- a/*/*.bak",
//	                         "+ a/*/*"]
//
// A file is included iff it matches the positive include AND none of the
// excludes — exactly the bash-shell semantics for !(...).
//
// Nested parens are not supported; "!(..." with no closing ")" is
// treated as a literal. Unmatched "!(...)" outside a recognized form is
// passed through unchanged (rclone will reject if it's invalid).
func expandGlobPattern(pattern string) []string {
	occs := findExtglobNegations(pattern)
	if len(occs) == 0 {
		return []string{"+ " + pattern}
	}
	rules := make([]string, 0, len(occs)+1)
	for i, occ := range occs {
		// Bash treats empty alternation slots as literal-empty (matching
		// nothing useful here); strings.Split keeps them, which is fine
		// — rclone will just not match an empty pattern. We don't dedupe.
		for _, alt := range strings.Split(occ.inner, "|") {
			sub := map[int]string{i: alt}
			rules = append(rules, "- "+replaceNegations(pattern, occs, sub))
		}
	}
	rules = append(rules, "+ "+replaceNegations(pattern, occs, nil))
	return rules
}

type extglobOccurrence struct {
	start int    // index of '!'
	end   int    // index just after the matching ')'
	inner string // content between '(' and ')' (excludes the parens)
}

// findExtglobNegations scans pattern for `!(...)` occurrences, left to
// right, non-overlapping. Only the simple form is recognized: a literal
// "!(", an inner that does NOT contain '(' or ')', and a literal ")".
// Anything more exotic (nested parens, escaped specials) is left as-is.
func findExtglobNegations(pattern string) []extglobOccurrence {
	var out []extglobOccurrence
	i := 0
	for i < len(pattern)-1 {
		if pattern[i] != '!' || pattern[i+1] != '(' {
			i++
			continue
		}
		j := strings.IndexByte(pattern[i+2:], ')')
		if j < 0 {
			break // unterminated — treat the rest as literal
		}
		end := i + 2 + j + 1
		inner := pattern[i+2 : end-1]
		if strings.ContainsAny(inner, "()") {
			// Nested or unbalanced — skip this position, keep scanning
			// past the '!' so we don't infinite-loop.
			i++
			continue
		}
		out = append(out, extglobOccurrence{start: i, end: end, inner: inner})
		i = end
	}
	return out
}

// replaceNegations rebuilds pattern with each `!(...)` occurrence
// replaced by either its substitute (from sub, keyed by occurrence
// index) or "*" if no substitute is provided. The "*" form is what
// produces the positive include rule; substitutes produce excludes
// (one per alternative).
func replaceNegations(pattern string, occs []extglobOccurrence, sub map[int]string) string {
	if len(occs) == 0 {
		return pattern
	}
	var b strings.Builder
	last := 0
	for i, occ := range occs {
		b.WriteString(pattern[last:occ.start])
		if alt, ok := sub[i]; ok {
			b.WriteString(alt)
		} else {
			b.WriteByte('*')
		}
		last = occ.end
	}
	b.WriteString(pattern[last:])
	return b.String()
}
