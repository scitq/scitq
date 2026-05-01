package fetch

import "testing"

func TestAppendDirWildcard(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "directory without action gets wildcard",
			in:   "azswed://rnd/path/",
			want: "azswed://rnd/path/*",
		},
		{
			name: "file without action unchanged",
			in:   "s3://rnd/foo.tgz",
			want: "s3://rnd/foo.tgz",
		},
		{
			name: "directory with mv action: wildcard goes on path, action preserved",
			in:   "azswed://rnd/path/|mv:foo",
			want: "azswed://rnd/path/*|mv:foo",
		},
		{
			name: "file with mv action whose target has trailing slash: action untouched",
			in:   "s3://rnd/foo.go|mv:./toto/",
			want: "s3://rnd/foo.go|mv:./toto/",
		},
		{
			name: "directory with mv action whose target has trailing slash: only path gets *",
			in:   "azswed://rnd/path/|mv:bowtie2/",
			want: "azswed://rnd/path/*|mv:bowtie2/",
		},
		{
			name: "directory with multiple actions chain unchanged",
			in:   "azswed://rnd/dir/|gunzip|mv:foo",
			want: "azswed://rnd/dir/*|gunzip|mv:foo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendDirWildcard(tc.in)
			if got != tc.want {
				t.Errorf("appendDirWildcard(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUriToRcloneString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "azswed directory with trailing slash",
			in:   "azswed://rnd/resource/metaphlan/mpa_vJan21_CHOCOPhlAnSGB_202103/bowtie2/",
			want: "azswed:rnd/resource/metaphlan/mpa_vJan21_CHOCOPhlAnSGB_202103/bowtie2/",
		},
		{
			name: "azswed file",
			in:   "azswed://rnd/resource/igc2.tgz",
			want: "azswed:rnd/resource/igc2.tgz",
		},
		{
			name: "absolute local path directory",
			in:   "/scratch/resources/abc/bowtie2/",
			want: "/scratch/resources/abc/bowtie2/",
		},
		{
			name: "absolute local path file",
			in:   "/scratch/resources/abc/index.tgz",
			want: "/scratch/resources/abc/index.tgz",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uri, err := ParseURI(tc.in)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := uriToRcloneString(*uri)
			if got != tc.want {
				t.Errorf("uriToRcloneString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
