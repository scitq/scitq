package client

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"unicode/utf8"
)

// maxLogLine is the longest log line sent; a longer line is sent in pieces.
const maxLogLine = 16 * 1024

// scanLogLines splits task output into lines ended by \n, \r or \r\n, so a
// progress bar redrawn with \r gives one line per redraw. A line longer
// than maxLogLine is cut, on a UTF-8 character boundary.
func scanLogLines(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	i := bytes.IndexAny(data, "\r\n")
	switch {
	case i >= 0 && i <= maxLogLine:
		if data[i] == '\r' {
			if i+1 == len(data) && !atEOF {
				return 0, nil, nil // is a \n next?
			}
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
		}
		return i + 1, data[:i], nil
	case len(data) > maxLogLine:
		cut := maxLogLine
		for j := cut; j > cut-utf8.UTFMax && j > 0; j-- {
			if utf8.RuneStart(data[j]) {
				cut = j
				break
			}
		}
		return cut, data[:cut], nil
	case atEOF:
		return len(data), data, nil
	}
	return 0, nil, nil
}

// readLogLines calls send for each line of r, until r ends. Lines are
// valid UTF-8 (gRPC refuses to send anything else). Once send fails, the
// rest of r is read and dropped: a task whose output is no longer read
// blocks on its next write and never ends.
func readLogLines(r io.Reader, send func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Split(scanLogLines)
	for scanner.Scan() {
		if err := send(strings.ToValidUTF8(scanner.Text(), "�")); err != nil {
			io.Copy(io.Discard, r)
			return err
		}
	}
	return scanner.Err()
}
