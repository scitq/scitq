package banner

import (
	"log"
	"os"
	"path/filepath"

	"github.com/scitq/scitq/internal/version"
)

func init() {
	app := filepath.Base(os.Args[0]) // e.g., scitq-server, scitq-client, scitq
	log.Printf("%s %s", app, version.Full())
}
