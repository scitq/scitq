package fetch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func performAction(action string, uri *URI, fs *MetaFileSystem, isEarly bool) error {
	switch {
	case action == "gunzip":
		return gunzip(uri, fs, isEarly)
	case action == "untar":
		return untar(uri, fs, isEarly)
	case strings.HasPrefix(action, "mv:"):
		return move(uri, isEarly, action[3:])
	case strings.HasPrefix(action, "rename:"):
		return renameAction(uri, isEarly, action[7:])
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}
}

func gunzip(uri *URI, fs *MetaFileSystem, isEarly bool) error {
	switch v := fs.fs.(type) {
	case *LocalBackend:
		{
			if isEarly {
				return nil
			}

			// Prefer pigz if available, fallback to gunzip otherwise
			cmdName, err := exec.LookPath("pigz")
			if err != nil {
				cmdName = "gunzip"
			}
			srcPath, err := v.AbsolutePath(uri.Path + uri.Separator + uri.File)
			if err != nil {
				return fmt.Errorf("error while getting path: %w", err)
			}
			cmd := exec.Command(cmdName, "-d", srcPath)

			// Run the command
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to run %s: %w", cmdName, err)
			}
			return nil
		}
	default:
		return fmt.Errorf("unsupported file system: %T", v)
	}
}

func untar(uri *URI, fs *MetaFileSystem, isEarly bool) error {
	switch v := fs.fs.(type) {
	case *LocalBackend:
		{
			if isEarly {
				return nil
			}

			srcPath, err := v.AbsolutePath(uri.Path + uri.Separator + uri.File)
			if err != nil {
				return fmt.Errorf("error while getting path: %w", err)
			}

			destDir := filepath.Dir(srcPath)
			cmd := exec.Command("tar", "-C", destDir, "-xf", srcPath)
			// Also set the process working directory as a safety net
			cmd.Dir = destDir
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to run tar in %s: %w", destDir, err)
			}

			// Remove the archive itself after successful extraction
			if err := os.Remove(srcPath); err != nil {
				return fmt.Errorf("failed to delete archive %s: %w", srcPath, err)
			}

			return nil
		}
	default:
		return fmt.Errorf("unsupported file system: %T", v)
	}
}

// move redirects where the destination URI points to *before* the copy
// runs (early phase), so the file lands at `<dst>/<target>/<file>` (or
// the absolute path if `target` starts with `/`). The late phase is a
// no-op — `mv:` is purely a pre-copy redirect, not an actual filesystem
// move post-download.
//
// Target normalisation:
//   - leading `./` is stripped, so `mv:./foo` is equivalent to `mv:foo`
//     (forgiving — it's the same user intent).
//   - any `../` segment is rejected — `mv:` is not allowed to escape the
//     download root (matches the docs in cli.md and the parser-time
//     check for relative paths in URI.Path).
func move(uri *URI, isEarly bool, target string) error {
	if !isEarly {
		return nil
	}
	target = strings.TrimPrefix(target, "./")
	if target == "" {
		return fmt.Errorf("mv target is empty after normalisation")
	}
	if strings.Contains("/"+target+"/", "/../") {
		return fmt.Errorf("mv target %q contains a parent reference (..); only subfolders are allowed", target)
	}
	if strings.HasPrefix(target, "/") {
		uri.Path = target
	} else {
		uri.Path = uri.Path + uri.Separator + target
	}
	return nil
}

// renameAction applies a Perl-style `s/<pattern>/<replacement>/<flags>`
// substitution to the URI's `File` (basename). Pre-copy / early phase only
// — the URI is rewritten before the transfer so the file lands at the new
// name; there is no separate late phase to move an already-copied file.
//
// Pattern syntax: `s/<regex>/<replacement>/<flags>`. The delimiter is taken
// from the second character of the spec (typically `/`), so an alternative
// like `s#a/b#x/y#` is also accepted when the pattern contains literal
// slashes. Flags:
//
//	g    apply to every match (default: first only)
//	i    case-insensitive match
//
// Replacement supports Go's regexp Expand backreferences (`$1`, `$2`,
// `${name}`).
//
// `|` is the URI action separator and is therefore not allowed inside a
// pattern. Use a character class like `[|]` if you genuinely need the
// literal character.
func renameAction(uri *URI, isEarly bool, spec string) error {
	if !isEarly {
		return nil
	}
	if len(spec) < 4 || spec[0] != 's' {
		return fmt.Errorf("rename spec must start with 's<delim>...' (got %q)", spec)
	}
	delim := spec[1]
	parts, err := splitPerlSub(spec[2:], delim)
	if err != nil {
		return fmt.Errorf("rename: %w (spec %q)", err, spec)
	}
	if len(parts) != 3 {
		return fmt.Errorf("rename spec %q must have form s%c<pattern>%c<replacement>%c<flags>",
			spec, delim, delim, delim)
	}
	pattern, replacement, flags := parts[0], parts[1], parts[2]

	if strings.ContainsAny(flags, "gi") == false && flags != "" {
		return fmt.Errorf("rename: unknown flags %q (supported: g, i)", flags)
	}
	for _, f := range flags {
		if f != 'g' && f != 'i' {
			return fmt.Errorf("rename: unknown flag %q (supported: g, i)", f)
		}
	}

	if strings.Contains(flags, "i") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("rename: invalid regex %q: %w", pattern, err)
	}

	if uri.File == "" {
		// No basename to rename (directory transfer). Treat as no-op rather
		// than an error — the same rename action attached to a glob source
		// should still be valid for the per-file transfer.
		return nil
	}

	if strings.Contains(flags, "g") {
		uri.File = re.ReplaceAllString(uri.File, replacement)
	} else {
		// Single replacement only — find first match, rewrite that span.
		loc := re.FindStringIndex(uri.File)
		if loc == nil {
			return nil
		}
		head := uri.File[:loc[0]]
		matched := uri.File[loc[0]:loc[1]]
		tail := uri.File[loc[1]:]
		uri.File = head + re.ReplaceAllString(matched, replacement) + tail
	}
	return nil
}

// splitPerlSub splits a `<pattern><delim><replacement><delim><flags>` string
// on the given delimiter, honouring backslash escapes (`\/` inside `s/.../.../`).
func splitPerlSub(s string, delim byte) ([]string, error) {
	parts := make([]string, 0, 3)
	cur := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == delim {
			cur = append(cur, delim)
			i += 2
			continue
		}
		if s[i] == delim {
			parts = append(parts, string(cur))
			cur = cur[:0]
			i++
			continue
		}
		cur = append(cur, s[i])
		i++
	}
	parts = append(parts, string(cur))
	return parts, nil
}
