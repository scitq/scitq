package main

import (
	"flag"
	"log"

	"github.com/gmtsciencedev/scitq2/server"
)

func main() {
	dbURL := flag.String("dburl", "postgres://scitq_user:dsofposiudipopipII9@localhost/scitq2?sslmode=disable", "db URL like postgres://user:pass@host/dbname?sslmode=disable")
	logRoot := flag.String("logroot", "log", "Root of log files")
	port := flag.Int("port", 50051, "Server port")
	certificatePem := flag.String("pem", "", "Certificate, PEM file, leave blank to use embedded")
	certificateKey := flag.String("key", "", "Certificate, key file, leave blank to use embedded")

	if err := server.Serve(*dbURL, *logRoot, *port, "", *certificateKey, *certificatePem); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
