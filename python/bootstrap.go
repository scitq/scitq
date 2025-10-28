package python

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Bootstrap ensures a valid Python venv with scitq2 installed.
func Bootstrap(venvPath string) error {

	// 1. Create venv if missing
	venvPython := filepath.Join(venvPath, "bin", "python")
	if _, err := os.Stat(venvPython); os.IsNotExist(err) {
		// ensure parent dir exists
		if err := os.MkdirAll(filepath.Dir(venvPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for venv: %w", err)
		}

		fmt.Printf("Creating Python venv at %s...\n", venvPath)
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
		fmt.Println("Installing pip in venv...")
		if err := exec.Command(venvPython, "-m", "ensurepip").Run(); err != nil {
			return fmt.Errorf("failed to install pip: %w", err)
		}
	}

	// Make sure pip, setuptools, and wheel exist and are up to date
	fmt.Println("Ensuring pip and setuptools in venv...")
	upgrade := exec.Command(venvPython, "-m", "pip", "install", "--upgrade", "pip", "setuptools", "wheel")
	upgrade.Stdout, upgrade.Stderr = os.Stdout, os.Stderr
	if err := upgrade.Run(); err != nil {
		return fmt.Errorf("failed to ensure pip/setuptools: %w", err)
	}

	cmd := exec.Command(pip, "install", "--upgrade", "--no-build-isolation", tmpDir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install scitq2 failed: %w", err)
	}

	// 5. Sanity check import
	check := exec.Command(venvPython, "-c", "import scitq2, grpc; print('Python DSL ready')")
	checkOut, err := check.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to import scitq2 or grpc: %v\nOutput:\n%s", err, checkOut)
	}
	fmt.Print(string(checkOut))
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
