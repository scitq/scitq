package fetch

import (
	"fmt"
	"os"
	"os/exec"
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
				return fmt.Errorf("error while getting path to %s", err)
			}
			cmd := exec.Command(cmdName, "-d", srcPath)

			// Run the command
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to run %s: %w", cmdName, err)
			}
			return nil
		}
	default:
		return fmt.Errorf("unsupported file system: %s", v)
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
				return fmt.Errorf("error while getting path to %w", err)
			}

			cmd := exec.Command("tar", "xf", srcPath)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to run tar: %w", err)
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

// move moves the file to the target location (simply modifying the target URI)
func move(uri *URI, isEarly bool, target string) error {
	if isEarly {
		if strings.HasPrefix(target, "/") {
			uri.Path = target
		} else {
			uri.Path = uri.Path + uri.Separator + target
		}
		return nil
	} else {
		return nil
	}
}
