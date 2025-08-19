package server

import (
	"crypto/tls"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

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

	// Serve client binary
	mux.HandleFunc("/scitq-client", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token != cfg.Scitq.ClientDownloadToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=scitq-client")
		http.ServeFile(w, r, cfg.Scitq.ClientBinaryPath)
	})

	// Serve embedded Svelte frontend
	sub, err := fs.Sub(publicFiles, "public")
	if err != nil {
		log.Fatalf("failed to create sub FS for public/: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	return tlsCert, certPEMstring, mux, nil
}
