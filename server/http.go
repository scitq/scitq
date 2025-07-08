package server

import (
	"crypto/tls"
	"embed"
	"log"
	"net/http"
	"os"

	"github.com/scitq/scitq/server/config"
)

//go:embed public/*
var publicFiles embed.FS

func HttpServer(cfg config.Config) (string, error) {
	// Retrieve certificate data from environment, if provided.
	var tlsCert tls.Certificate
	var certPEMstring string
	var err error
	if cfg.Scitq.CertificateKey == "" || cfg.Scitq.CertificatePem == "" {
		log.Printf("Using embedded certificates")
		tlsCert, certPEMstring, err = LoadEmbeddedCertificates()
		if err != nil {
			log.Fatalf("failed to load embedded TLS credentials: %v", err)
		}
	} else {
		// Read the certificate and key file contents.
		certPEMData, err := os.ReadFile(cfg.Scitq.CertificatePem)
		if err != nil {
			log.Fatalf("failed to read certificate file: %v", err)
		}
		certPEMstring = string(certPEMData)
		certKeyData, err := os.ReadFile(cfg.Scitq.CertificateKey)
		if err != nil {
			log.Fatalf("failed to read certificate key file: %v", err)
		}
		tlsCert, err = tls.X509KeyPair(certPEMData, certKeyData)
		if err != nil {
			log.Fatalf("failed to load TLS credentials from file: %v", err)
		}
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	// Endpoint to serve the client binary over HTTPS.
	http.HandleFunc("/scitq-client", func(w http.ResponseWriter, r *http.Request) {
		// Check for the correct token.
		token := r.URL.Query().Get("token")
		if token != cfg.Scitq.ClientDownloadToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Set headers to indicate a binary download.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=scitq-client")
		http.ServeFile(w, r, cfg.Scitq.ClientBinaryPath)
	})

	// Serve the Svelte application assets from the embedded "public" folder.
	fs := http.FileServer(http.FS(publicFiles))
	http.Handle("/", fs)

	// Create an HTTP server with the TLS configuration.
	server := &http.Server{
		Addr:      ":443",
		TLSConfig: tlsConfig,
	}

	log.Printf("Starting HTTPS server")
	return certPEMstring, server.ListenAndServeTLS("", "")
}
