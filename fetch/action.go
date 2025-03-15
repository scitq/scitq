package fetch

import (
	"fmt"
	"os"
	"os/exec"
)

func performAction(action string, uri URI, fs *MetaFileSystem, isEarly bool) error {
	switch action {
	case "gunzip":
		return gunzip(uri, fs, isEarly)
	case "untar":
		return untar(uri, fs, isEarly)
	default:
		return fmt.Errorf("unsupported action: %s", action)
	}
}

func gunzip(uri URI, fs *MetaFileSystem, isEarly bool) error {
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

func untar(uri URI, fs *MetaFileSystem, isEarly bool) error {
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
