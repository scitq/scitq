package client

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func readAll(t *testing.T, input string) []string {
	t.Helper()
	var lines []string
	if err := readLogLines(strings.NewReader(input), func(l string) error {
		lines = append(lines, l)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestLogLinesEndOnNewlineOrCarriageReturn(t *testing.T) {
	got := readAll(t, "a\nb\r\nc\rd\r\re")
	want := []string{"a", "b", "c", "d", "", "e"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestProgressBarIsReadPastScannerLimit(t *testing.T) {
	// 200 KiB of progress bar redraws, then a final line: well past the
	// 64 KiB a default bufio.Scanner holds.
	bar := strings.Repeat("[=====>    ] 42%\r", 12000)
	got := readAll(t, bar+"done\n")
	if len(got) != 12001 || got[len(got)-1] != "done" {
		t.Fatalf("got %d lines, last %q", len(got), got[len(got)-1])
	}
}

func TestLongLineIsCutOnCharacterBoundary(t *testing.T) {
	// "é" is two bytes; an odd prefix puts maxLogLine in the middle of one.
	input := "x" + strings.Repeat("é", maxLogLine) + "\n"
	got := readAll(t, input)
	if len(got) < 2 {
		t.Fatalf("a %d-byte line was not cut", len(input))
	}
	for _, l := range got {
		if len(l) > maxLogLine || !utf8.ValidString(l) {
			t.Fatalf("piece of %d bytes, valid UTF-8 %v", len(l), utf8.ValidString(l))
		}
	}
	if strings.Join(got, "")+"\n" != input {
		t.Fatal("pieces do not add up to the line")
	}
}

func TestInvalidUTF8IsReplaced(t *testing.T) {
	if got := readAll(t, "ok\xff\xfe\n"); got[0] != "ok�" {
		t.Fatalf("got %q", got[0])
	}
}

// A task writing to a pipe blocks once the pipe is full and nobody reads
// it. After a failed send the reader must keep emptying the pipe, or the
// task never ends.
func TestPipeIsEmptiedAfterSendFails(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go readLogLines(r, func(string) error { return errors.New("stream closed") })

	written := make(chan error, 1)
	go func() {
		// Far more than a pipe buffer (64 KiB on Linux, 16 KiB on macOS).
		_, err := w.Write([]byte(strings.Repeat("progress 42%\r", 100000)))
		w.Close()
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the writer is blocked: the pipe is no longer read")
	}
}
