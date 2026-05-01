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
