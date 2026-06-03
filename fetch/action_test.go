package fetch

import (
	"strings"
	"testing"
)

func TestMoveActionEarlyPhase(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		startURI URI
		wantPath string
		wantErr  string
	}{
		{
			name:     "plain subfolder",
			target:   "toto",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantPath: "scratch/abc/toto",
		},
		{
			name:     "subfolder with trailing slash",
			target:   "toto/",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantPath: "scratch/abc/toto/",
		},
		{
			name:     "leading ./ is stripped",
			target:   "./toto/",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantPath: "scratch/abc/toto/",
		},
		{
			name:     "absolute target replaces path",
			target:   "/abs/path",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantPath: "/abs/path",
		},
		{
			name:    "parent reference rejected",
			target:  "../escape",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantErr: "parent reference",
		},
		{
			name:    "mid-path parent reference rejected",
			target:  "foo/../bar",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantErr: "parent reference",
		},
		{
			name:    "empty after normalisation rejected",
			target:  "./",
			startURI: URI{Path: "scratch/abc", Separator: "/"},
			wantErr: "empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri := tc.startURI
			err := move(&uri, true, tc.target)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (uri.Path=%q)", tc.wantErr, uri.Path)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if uri.Path != tc.wantPath {
				t.Errorf("Path = %q, want %q", uri.Path, tc.wantPath)
			}
		})
	}
}

func TestMoveActionLatePhaseIsNoop(t *testing.T) {
	uri := URI{Path: "scratch/abc", Separator: "/"}
	original := uri.Path
	if err := move(&uri, false, "anything"); err != nil {
		t.Fatalf("late phase should not error: %v", err)
	}
	if uri.Path != original {
		t.Errorf("late phase mutated path: %q -> %q", original, uri.Path)
	}
}

func TestRenameActionEarlyPhase(t *testing.T) {
	cases := []struct {
		name     string
		spec     string
		startURI URI
		wantFile string
		wantErr  string
	}{
		{
			name:     "simple substitution renames basename",
			spec:     "s/quality_report/checkm2_quality_report/",
			startURI: URI{Path: "results", File: "quality_report.tsv", Separator: "/"},
			wantFile: "checkm2_quality_report.tsv",
		},
		{
			name:     "global flag replaces every match",
			spec:     "s/x/y/g",
			startURI: URI{Path: "results", File: "xxxx", Separator: "/"},
			wantFile: "yyyy",
		},
		{
			name:     "no global flag replaces only first match",
			spec:     "s/x/y/",
			startURI: URI{Path: "results", File: "xxxx", Separator: "/"},
			wantFile: "yxxx",
		},
		{
			name:     "case-insensitive flag",
			spec:     "s/REPORT/result/i",
			startURI: URI{Path: "results", File: "Report.tsv", Separator: "/"},
			wantFile: "result.tsv",
		},
		{
			name:     "backreference in replacement",
			spec:     `s/(.*)\.tsv/$1.csv/`,
			startURI: URI{Path: "results", File: "data.tsv", Separator: "/"},
			wantFile: "data.csv",
		},
		{
			name:     "alternative delimiter (hash) for paths with slashes",
			spec:     "s#prefix_#X_#",
			startURI: URI{Path: "results", File: "prefix_data.tsv", Separator: "/"},
			wantFile: "X_data.tsv",
		},
		{
			name:     "no match leaves filename unchanged",
			spec:     "s/nope/yes/",
			startURI: URI{Path: "results", File: "data.tsv", Separator: "/"},
			wantFile: "data.tsv",
		},
		{
			name:     "directory transfer (empty File) is no-op",
			spec:     "s/x/y/",
			startURI: URI{Path: "results", File: "", Separator: "/"},
			wantFile: "",
		},
		{
			name:     "anchored pattern",
			spec:     `s/^pre//`,
			startURI: URI{Path: "results", File: "pre_data.tsv", Separator: "/"},
			wantFile: "_data.tsv",
		},
		{
			name:     "missing 's' prefix rejected",
			spec:     "/foo/bar/",
			startURI: URI{Path: "results", File: "foo.tsv", Separator: "/"},
			wantErr:  "must start with 's<delim>",
		},
		{
			name:     "unknown flag rejected",
			spec:     "s/a/b/x",
			startURI: URI{Path: "results", File: "a.tsv", Separator: "/"},
			wantErr:  "unknown flag",
		},
		{
			name:     "invalid regex rejected",
			spec:     "s/[invalid/foo/",
			startURI: URI{Path: "results", File: "a.tsv", Separator: "/"},
			wantErr:  "invalid regex",
		},
		{
			name:     "missing replacement section rejected",
			spec:     "s/foo/",
			startURI: URI{Path: "results", File: "foo.tsv", Separator: "/"},
			wantErr:  "must have form",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri := tc.startURI
			err := renameAction(&uri, true, tc.spec)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (uri.File=%q)", tc.wantErr, uri.File)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if uri.File != tc.wantFile {
				t.Errorf("File = %q, want %q", uri.File, tc.wantFile)
			}
		})
	}
}

func TestRenameActionLatePhaseIsNoop(t *testing.T) {
	uri := URI{Path: "results", File: "data.tsv", Separator: "/"}
	if err := renameAction(&uri, false, "s/data/whatever/"); err != nil {
		t.Fatalf("late phase should not error: %v", err)
	}
	if uri.File != "data.tsv" {
		t.Errorf("late phase mutated file: %q", uri.File)
	}
}

func TestRenameViaPerformAction(t *testing.T) {
	// performAction is the dispatch the URI parser plugs into.
	uri := URI{Path: "results", File: "quality_report.tsv", Separator: "/"}
	if err := performAction("rename:s/quality_report/final/", &uri, nil, true); err != nil {
		t.Fatalf("performAction: %v", err)
	}
	if uri.File != "final.tsv" {
		t.Errorf("File = %q, want %q", uri.File, "final.tsv")
	}
}

func TestSplitPerlSub(t *testing.T) {
	cases := []struct {
		in    string
		delim byte
		want  []string
	}{
		{"foo/bar/g", '/', []string{"foo", "bar", "g"}},
		{`a\/b/c/`, '/', []string{"a/b", "c", ""}},
		{"foo|bar|", '|', []string{"foo", "bar", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := splitPerlSub(tc.in, tc.delim)
			if err != nil {
				t.Fatalf("splitPerlSub: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
