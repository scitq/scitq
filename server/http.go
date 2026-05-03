package server

import (
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/scitq/scitq/server/config"
)

//go:embed all:public
var publicFiles embed.FS

func HttpServer(cfg config.Config) (tls.Certificate, string, http.Handler, error) {
	var tlsCert tls.Certificate
	var certPEMstring string
	var err error

	if cfg.Scitq.CertificateKey == "" || cfg.Scitq.CertificatePem == "" {
		log.Printf("Using embedded certificates")
		tlsCert, certPEMstring, err = LoadEmbeddedCertificates()
		if err != nil {
			return tls.Certificate{}, "", nil, fmt.Errorf("failed to load embedded TLS credentials: %w", err)
		}
	} else {
		certPEMData, err := os.ReadFile(cfg.Scitq.CertificatePem)
		if err != nil {
			return tls.Certificate{}, "", nil, fmt.Errorf("failed to read certificate file: %w", err)
		}
		certPEMstring = string(certPEMData)
		certKeyData, err := os.ReadFile(cfg.Scitq.CertificateKey)
		if err != nil {
			return tls.Certificate{}, "", nil, fmt.Errorf("failed to read certificate key file: %w", err)
		}
		tlsCert, err = tls.X509KeyPair(certPEMData, certKeyData)
		if err != nil {
			return tls.Certificate{}, "", nil, fmt.Errorf("failed to load TLS credentials from file: %w", err)
		}
	}

	mux := http.NewServeMux()

	// Either of these tokens authorizes a client-binary download. The
	// download token is for cloud-init bootstrap (it's the only secret a
	// freshly-provisioned VM has). The worker token is for already-running
	// workers fetching an upgraded binary on their own — they already
	// authenticate every gRPC call with it, so there's no privilege boost
	// in accepting it here. See specs/worker_autoupgrade.md (Phase II).
	authorizedDownload := func(r *http.Request) bool {
		token := r.URL.Query().Get("token")
		return token != "" && (token == cfg.Scitq.ClientDownloadToken || token == cfg.Scitq.WorkerToken)
	}

	// Serve client binary
	mux.HandleFunc("/scitq-client", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedDownload(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=scitq-client")
		http.ServeFile(w, r, cfg.Scitq.ClientBinaryPath)
	})

	// Phase II of worker auto-upgrade: workers verify the binary they
	// just downloaded against this hash before atomic rename. The hash
	// is cached by (mtime,size) of the binary so repeat calls don't
	// re-hash 30 MB.
	mux.HandleFunc("/scitq-client.sha256", func(w http.ResponseWriter, r *http.Request) {
		if !authorizedDownload(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		hash, err := clientBinaryHash(cfg.Scitq.ClientBinaryPath)
		if err != nil {
			http.Error(w, "client binary unavailable", http.StatusInternalServerError)
			log.Printf("⚠️ /scitq-client.sha256: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=us-ascii")
		fmt.Fprintln(w, hash)
	})

	// Serve embedded Svelte frontend
	sub, err := fs.Sub(publicFiles, "public")
	if err != nil {
		log.Fatalf("failed to create sub FS for public/: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	return tlsCert, certPEMstring, mux, nil
}

// clientBinaryHash returns the SHA-256 hex digest of the file at path.
// Cached by (mtime, size) so repeat calls don't re-hash; the cache is
// invalidated automatically when the operator drops in a new binary
// (mtime/size changes).
func clientBinaryHash(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	key := clientBinaryCacheKey{mtime: st.ModTime().UnixNano(), size: st.Size()}

	clientBinaryHashCache.mu.RLock()
	if clientBinaryHashCache.key == key && clientBinaryHashCache.hash != "" {
		hash := clientBinaryHashCache.hash
		clientBinaryHashCache.mu.RUnlock()
		return hash, nil
	}
	clientBinaryHashCache.mu.RUnlock()

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	hash := hex.EncodeToString(h.Sum(nil))

	clientBinaryHashCache.mu.Lock()
	clientBinaryHashCache.key = key
	clientBinaryHashCache.hash = hash
	clientBinaryHashCache.mu.Unlock()

	return hash, nil
}

type clientBinaryCacheKey struct {
	mtime int64
	size  int64
}

var clientBinaryHashCache struct {
	mu   sync.RWMutex
	key  clientBinaryCacheKey
	hash string
}
