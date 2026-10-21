package server

import "testing"

func TestNormalizeFolderPath(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"/", "", false},
		{"///", "", false}, // all slashes collapse to root
		{"bio", "bio", false},
		{"bio/qc", "bio/qc", false},
		{"/bio/qc/", "bio/qc", false},
		{"bio//qc", "", true},
		{"bio/qc/fastp", "bio/qc/fastp", false},
		{"a_b-c.d", "a_b-c.d", false},
		{"my folder/sub", "my folder/sub", false},
		{"bio;rm -rf /", "", true},
		{"bio\x00qc", "", true},
	}
	for _, tc := range cases {
		got, err := normalizeFolderPath(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeFolderPath(%q) expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeFolderPath(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeFolderPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
