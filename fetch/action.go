package fetch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
