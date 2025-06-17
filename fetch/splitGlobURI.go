package fetch

import (
	"errors"
	"strings"
)

// splitGlobURI separates a full URI into a base path and a glob pattern.
// Example:
// "azure://bucket/data/**/*.fastq.gz" → "azure://bucket/data/", "**/*.fastq.gz"
func splitGlobURI(uri string) (baseURI, pattern string, err error) {
	segments := strings.Split(uri, "/")

	for i, seg := range segments {
		if strings.Contains(seg, "*") {
			if i == 0 {
				return "", "", errors.New("invalid glob URI: wildcard in scheme or bucket")
			}
			baseURI = strings.Join(segments[:i], "/") + "/"
			pattern = strings.Join(segments[i:], "/")
			return baseURI, pattern, nil
		}
	}

	// No glob detected
	return uri, "", nil
}

func detectGlob(path string) (base string, pattern string, hasGlob bool) {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.Contains(part, "*") {
			base = strings.Join(parts[:i], "/")
			pattern = strings.Join(parts[i:], "/")
			return base, pattern, true
		}
	}
	return path, "", false
}
