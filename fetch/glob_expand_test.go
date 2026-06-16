package fetch

import (
	"reflect"
	"testing"
)

func TestExpandGlobPattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "no negation, simple",
			pattern: "*.fastq.gz",
			want:    []string{"+ *.fastq.gz"},
		},
		{
			name:    "no negation, recursive",
			pattern: "**/*.tgz",
			want:    []string{"+ **/*.tgz"},
		},
		{
			name:    "single negation",
			pattern: "data/!(*.tgz)",
			want:    []string{"- data/*.tgz", "+ data/*"},
		},
		{
			name:    "single negation with alternation",
			pattern: "**/!(*.tgz|*.zip)",
			want:    []string{"- **/*.tgz", "- **/*.zip", "+ **/*"},
		},
		{
			name:    "negation at root",
			pattern: "!(*.tgz)",
			want:    []string{"- *.tgz", "+ *"},
		},
		{
			name:    "two negations, different segments",
			pattern: "a/!(*.tgz)/!(*.bak)",
			want: []string{
				"- a/*.tgz/*", // exclude tgz dirs (any name follows)
				"- a/*/*.bak", // exclude .bak files
				"+ a/*/*",
			},
		},
		{
			name:    "unterminated !( is literal",
			pattern: "data/!(unclosed",
			want:    []string{"+ data/!(unclosed"},
		},
		{
			name:    "nested parens skipped (left as literal)",
			pattern: "data/!(foo(bar))",
			// findExtglobNegations rejects this; entire pattern is the
			// positive include — rclone will then reject it if invalid,
			// which is the right failure mode (loud, not silent
			// mis-translation).
			want: []string{"+ data/!(foo(bar))"},
		},
		{
			name:    "single-alternative",
			pattern: "!(*.tgz|)",
			// Empty alternative becomes an empty exclude pattern; rclone
			// will treat "- " as a no-op rule. Acceptable.
			want: []string{"- *.tgz", "- ", "+ *"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandGlobPattern(tc.pattern)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expandGlobPattern(%q)\n  got:  %#v\n  want: %#v", tc.pattern, got, tc.want)
			}
		})
	}
}
