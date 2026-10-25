package server

import (
	"embed"
	"sort"
	"strings"
)

//go:embed docs/*.md
var embeddedDocs embed.FS

// docPages lists the documentation pages served by get_doc: every embedded
// docs/*.md file, by name without extension.
func docPages() []string {
	entries, _ := embeddedDocs.ReadDir("docs")
	var pages []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			pages = append(pages, name)
		}
	}
	sort.Strings(pages)
	return pages
}
