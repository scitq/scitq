package fetch

import (
	"context"
	"fmt"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

func getMD5(entry fs.DirEntry) (string, error) {
	// Convert DirEntry to fs.Object (file representation)
	ctx := context.Background()
	obj, ok := entry.(fs.Object)
	if !ok {
		return "", fmt.Errorf("entry is not a file, cannot compute MD5")
	}

	// Get MD5 hash using Rclone's library
	md5sum, err := obj.Hash(ctx, hash.MD5)
	if err != nil {
		return "", fmt.Errorf("failed to get MD5 hash: %v", err)
	}

	if md5sum == "" {
		return "", fmt.Errorf("MD5 hash not available for this backend")
	}

	return md5sum, nil
}
