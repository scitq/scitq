package server

import "embed"

//go:embed docs/*.md
var embeddedDocs embed.FS
