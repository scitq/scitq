package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseCommaSeparatedParams_PlainPairs is the existing baseline:
// simple k=v pairs separated by commas, no quoting tricks.
func TestParseCommaSeparatedParams_PlainPairs(t *testing.T) {
	js, err := parseCommaSeparatedParams("a=1,b=hello,c=s3://bucket/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"a": "1", "b": "hello", "c": "s3://bucket/path"}
	assertEqual(t, got, want)
}

// TestParseCommaSeparatedParams_QuotedComma is the case that motivated
// this whole helper: a value that contains commas, wrapped in double
// quotes so the splitter doesn't tear it apart.
func TestParseCommaSeparatedParams_QuotedComma(t *testing.T) {
	js, err := parseCommaSeparatedParams(`a=x,k_list="21,41,61,81,99",numa=1`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"a": "x", "k_list": "21,41,61,81,99", "numa": "1"}
	assertEqual(t, got, want)
}

// TestParseCommaSeparatedParams_SingleQuotedComma — same thing with '.
// Both quote styles are accepted; the parser tracks them independently
// so a value like `'has "double" quotes inside'` round-trips cleanly.
func TestParseCommaSeparatedParams_SingleQuotedComma(t *testing.T) {
	js, err := parseCommaSeparatedParams(`note='hello, world',n=42`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"note": "hello, world", "n": "42"}
	assertEqual(t, got, want)
}

// TestParseCommaSeparatedParams_NestedQuotes — a single-quoted value
// can carry a literal double-quote and vice versa.
func TestParseCommaSeparatedParams_NestedQuotes(t *testing.T) {
	js, err := parseCommaSeparatedParams(`a='he said "hi"',b="it's me"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"a": `he said "hi"`, "b": `it's me`}
	assertEqual(t, got, want)
}

// TestParseCommaSeparatedParams_UnmatchedQuote — surface the syntax
// error clearly instead of silently producing garbage.
func TestParseCommaSeparatedParams_UnmatchedQuote(t *testing.T) {
	_, err := parseCommaSeparatedParams(`a="missing close`)
	if err == nil {
		t.Fatalf("expected error on unmatched quote")
	}
	if !strings.Contains(err.Error(), "unmatched quote") {
		t.Errorf("expected 'unmatched quote' in error, got %v", err)
	}
}

// TestParseCommaSeparatedParams_FileShorthand — the @file expansion
// reads the local file and ships its content. Quoting and @file should
// compose: `param="@/path"` is just `@/path` after quote stripping.
func TestParseCommaSeparatedParams_FileShorthand(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "list.txt")
	content := "line1\nline2 with, comma\nline3\n"
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	js, err := parseCommaSeparatedParams("samples=@" + fp + ",x=y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["samples"] != content {
		t.Errorf("samples content mismatch: got %q, want %q", got["samples"], content)
	}
	if got["x"] != "y" {
		t.Errorf("x: got %q, want y", got["x"])
	}
}

// TestParseCommaSeparatedParams_FileShorthandQuoted — operator wraps
// the path in quotes (e.g. to keep their shell from globbing); strip
// the quotes before reading.
func TestParseCommaSeparatedParams_FileShorthandQuoted(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(fp, []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	js, err := parseCommaSeparatedParams(`samples="@` + fp + `"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["samples"] != "ok\n" {
		t.Errorf("content mismatch: got %q", got["samples"])
	}
}

// TestParseCommaSeparatedParams_EscapedAt — `\@literal` opts out of the
// file-content shorthand and passes a literal leading @ to the server.
func TestParseCommaSeparatedParams_EscapedAt(t *testing.T) {
	js, err := parseCommaSeparatedParams(`name=\@github_user`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["name"] != "@github_user" {
		t.Errorf("escaped @ not preserved: got %q", got["name"])
	}
}

// TestParseCommaSeparatedParams_FullMegahitCommand — the exact pattern
// the user wanted to write, end-to-end: @file shorthand on one param,
// quoted-comma list on another, plain values on the rest.
func TestParseCommaSeparatedParams_FullMegahitCommand(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "samples.txt")
	if err := os.WriteFile(fp, []byte("s3://bucket/SAMP1/*.fq.gz\ns3://bucket/SAMP2/*.fq.gz\n"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	cmd := `data_source=list,sample_list=@` + fp + `,output_uri=s3://rnd/results/SCAPIS/megahit/,k_list="21,41,61,81,99",numa=1`
	js, err := parseCommaSeparatedParams(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["data_source"] != "list" || got["k_list"] != "21,41,61,81,99" || got["numa"] != "1" {
		t.Errorf("unexpected map: %+v", got)
	}
	if !strings.Contains(got["sample_list"], "SAMP1") || !strings.Contains(got["sample_list"], "SAMP2") {
		t.Errorf("sample_list content not embedded: %q", got["sample_list"])
	}
}

// --- helpers ---

func mustUnmarshal(t *testing.T, js string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func assertEqual(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
}

// -- Repeatable --param form -------------------------------------------------

// TestParseParamEntries_RepeatablePairs — the new preferred shape:
// `--param a=1 --param b=2 --param c=3` produces the same map as the
// legacy single-string form. Verifies each entry is treated as one
// key=value.
func TestParseParamEntries_RepeatablePairs(t *testing.T) {
	js, err := parseParamEntries([]string{"a=1", "b=hello", "c=s3://bucket/path"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"a": "1", "b": "hello", "c": "s3://bucket/path"}
	assertEqual(t, got, want)
}

// TestParseParamEntries_CommaInSingleValue — the key motivation for
// repeatability: a value that itself contains commas ('depth=1x1,2,3')
// must survive without quoting when it's the only pair in the entry.
// The old comma-separated parser would have split this into three
// invalid pairs.
func TestParseParamEntries_CommaInSingleValue(t *testing.T) {
	js, err := parseParamEntries([]string{"depth=1x1,2,3,4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["depth"] != "1x1,2,3,4" {
		t.Errorf("comma-in-value not preserved: got %q", got["depth"])
	}
}

// TestParseParamEntries_LaterOverridesEarlier — repeatable --param
// wants "last one wins" for key collisions so a script can layer a
// defaults block then override a specific key. Locks the semantic in.
func TestParseParamEntries_LaterOverridesEarlier(t *testing.T) {
	js, err := parseParamEntries([]string{"env=staging", "env=prod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["env"] != "prod" {
		t.Errorf("last-wins broken: got env=%q, want prod", got["env"])
	}
}

// TestParseParamEntries_MixedForms — one entry is a legacy
// comma-separated batch, another is a single pair. Both merge into the
// same output map. Locks in that the two forms coexist safely.
func TestParseParamEntries_MixedForms(t *testing.T) {
	js, err := parseParamEntries([]string{"a=1,b=2", "c=3,4,5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"a": "1", "b": "2", "c": "3,4,5"}
	assertEqual(t, got, want)
}

// TestParseParamEntries_FileShorthandPerEntry — @file expansion still
// works when the entry is a single pair (not just the legacy shape),
// so `--param samples=@/path/to.list` reads the file as expected.
func TestParseParamEntries_FileShorthandPerEntry(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "samples.txt")
	if err := os.WriteFile(fp, []byte("A\nB\nC\n"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	js, err := parseParamEntries([]string{"samples=@" + fp, "n=42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	if got["samples"] != "A\nB\nC\n" {
		t.Errorf("@file on single-pair entry not expanded: got %q", got["samples"])
	}
	if got["n"] != "42" {
		t.Errorf("second entry lost: got %q", got["n"])
	}
}

// TestParseParamEntries_LegacySingleEntryStillWorks — the old
// single-string form (one --param carrying multiple k=v pairs) still
// parses through the repeatable-slice API. Guards against a regression
// where the legacy shape gets treated as one pair with a comma-carrying
// value.
func TestParseParamEntries_LegacySingleEntryStillWorks(t *testing.T) {
	js, err := parseParamEntries([]string{`a=x,k_list="21,41,61",numa=1`})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mustUnmarshal(t, js)
	want := map[string]string{"a": "x", "k_list": "21,41,61", "numa": "1"}
	assertEqual(t, got, want)
}

// -- ensure the imported strings package stays used across the file --
var _ = strings.Split
