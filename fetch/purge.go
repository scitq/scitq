package fetch

import (
	"context"
	"errors"
	"fmt"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/operations"
)

// Purge recursively removes everything under the given URI. Used by
// workflow-lifetime output cleanup (spec: outputs.lifetime = workflow):
// once a workflow reaches S, the server sweeps the workspace copies of
// intermediate outputs the author declared as ephemeral.
//
// Purge is idempotent-ish: rclone returns fs.ErrorDirNotFound (or the
// backend's equivalent) when the directory is already gone, which we
// treat as success. Any other error is bubbled up so the caller can
// log it / raise an event.
//
// The URI is resolved through the same ParseURI + uriToRcloneString
// path the copy code uses, so any backend rclone knows about (via
// /etc/rclone.conf) works. The trailing slash convention on the URI
// doesn't matter — Purge operates on a directory prefix either way.
func Purge(ctx context.Context, uriStr string) error {
	uri, err := ParseURI(uriStr)
	if err != nil {
		return fmt.Errorf("purge: parse %q: %w", uriStr, err)
	}
	remote := uriToRcloneString(*uri)
	rf, err := fs.NewFs(ctx, remote)
	if err != nil {
		// A non-existent bucket / bad remote name shouldn't be treated
		// as "already gone" — surface it so the operator sees the typo.
		return fmt.Errorf("purge: build fs for %q: %w", remote, err)
	}
	if err := operations.Purge(ctx, rf, ""); err != nil {
		if errors.Is(err, fs.ErrorDirNotFound) || errors.Is(err, fs.ErrorObjectNotFound) {
			return nil
		}
		return fmt.Errorf("purge %q: %w", remote, err)
	}
	return nil
}
