package python

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// bootstrapMu serialises concurrent Bootstrap calls within the same process
// (e.g. multiple test servers in the integration suite sharing one venv dir).
// Without it, two `pip install --upgrade` invocations race: pip uninstalls
// scitq2 to "upgrade" it while another already-finished bootstrap's caller
// is invoking python — and the script crashes on
// `ModuleNotFoundError: No module named 'scitq2.util'`. The fingerprint
// check below additionally short-circuits redundant reinstalls (when the
// embedded source hasn't changed since the last successful install) so the
// venv survives a server restart without a 30+s pip install.
var bootstrapMu sync.Mutex

// Bootstrap ensures a valid Python venv with scitq2 installed.
func Bootstrap(venvPath string) error {
	bootstrapMu.Lock()
	defer bootstrapMu.Unlock()

	// Skip the install entirely when an earlier Bootstrap on this venv path
	// already finished against the same embedded source. The fingerprint is
	// just sha256 of EmbeddedPythonSrc — different bytes → different
	// fingerprint → reinstall. Stored next to the venv so a fresh process
	// can also reuse it.
	sum := sha256.Sum256(EmbeddedPythonSrc)
	wantFingerprint := hex.EncodeToString(sum[:])
	markerPath := filepath.Join(venvPath, ".scitq2-bootstrap-fingerprint")
	if existing, err := os.ReadFile(markerPath); err == nil && string(existing) == wantFingerprint {
		return nil
	}

	// 1. Create venv if missing
	venvPython := filepath.Join(venvPath, "bin", "python")
	if _, err := os.Stat(venvPython); os.IsNotExist(err) {
		// ensure parent dir exists
		if err := os.MkdirAll(filepath.Dir(venvPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for venv: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Creating Python venv at %s...\n", venvPath)
		// Ensure Python is available
		if _, err := exec.LookPath("python3"); err != nil {
			return fmt.Errorf("python3 not found in PATH")
		}
		if err := exec.Command("python3", "-m", "venv", venvPath).Run(); err != nil {
			return fmt.Errorf("failed to create venv: %w", err)
		}
	}

	// 2. Extract embedded source to tmpdir
	tmpDir, err := os.MkdirTemp("", "scitq2-src-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if err := untarGzBytes(EmbeddedPythonSrc, tmpDir); err != nil {
		return fmt.Errorf("failed to extract embedded Python source: %w", err)
	}

	// 4. Ensure pip exists and install the embedded package (with dependencies)
	pip := filepath.Join(venvPath, "bin", "pip")
	if _, err := os.Stat(pip); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "Installing pip in venv...")
		if err := exec.Command(venvPython, "-m", "ensurepip").Run(); err != nil {
			return fmt.Errorf("failed to install pip: %w", err)
		}
	}

	// Make sure pip, setuptools, and wheel exist and are up to date
	fmt.Fprintln(os.Stderr, "Ensuring pip and setuptools in venv...")
	upgrade := exec.Command(venvPython, "-m", "pip", "install", "--upgrade", "pip", "setuptools", "wheel")
	upgrade.Stdout, upgrade.Stderr = os.Stderr, os.Stderr
	if err := upgrade.Run(); err != nil {
		return fmt.Errorf("failed to ensure pip/setuptools: %w", err)
	}

	cmd := exec.Command(pip, "install", "--upgrade", "--no-build-isolation", tmpDir+"[optuna]")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install scitq2 failed: %w", err)
	}

	// 5. Sanity check import
	check := exec.Command(venvPython, "-c", "import scitq2, grpc; print('Python DSL ready')")
	checkOut, err := check.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to import scitq2 or grpc: %v\nOutput:\n%s", err, checkOut)
	}
	fmt.Fprint(os.Stderr, string(checkOut))

	// Record the fingerprint of the embedded source we just installed so a
	// subsequent Bootstrap call (same process or next process startup) can
	// short-circuit. Best-effort: failure to write doesn't fail the bootstrap.
	if err := os.WriteFile(markerPath, []byte(wantFingerprint), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ failed to write bootstrap fingerprint: %v\n", err)
	}
	return nil
}

func untarGzBytes(data []byte, dst string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dst, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		default:
			continue
		}
	}
	return nil
}
