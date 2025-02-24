package fetch

import (
	"context"
	"fmt"
	"log"

	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/object"
	"github.com/rclone/rclone/fs/operations"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

func CopyFiles(srcFs fs.Fs, srcPath string, dstFs fs.Fs, dstPath string) error {
	// Create a context for the operation
	ctx := context.Background()
	// Copy the file
	err := operations.CopyFile(ctx, dstFs, srcFs, dstPath, srcPath)
	if err != nil {
		return fmt.Errorf("failed to copy file: %v", err)
	}

	return nil
}

func ListFiles(fsys fs.Fs, path string) error {
	// Create a context for the operation
	ctx := context.Background()

	// List the files in the directory
	dir, err := fsys.List(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to list files: %v", err)
	}

	// Print the file names
	for _, entry := range dir {
		fmt.Println(entry.Remote())
	}

	return nil
}

func CopyFilesWithProgress(srcFs fs.Fs, srcPath string, dstFs fs.Fs, dstPath string) error {
	// Create a context for the operation
	ctx := context.Background()

	// Open the source file
	srcFile, err := srcFs.NewObject(ctx, srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %v", err)
	}

	// Get the size of the source file
	size := srcFile.Size()

	// Create a new progress bar
	p := mpb.New()
	bar := p.New(int64(size),
		mpb.BarStyle().Lbound("|"),
		mpb.PrependDecorators(
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(
			decor.Percentage(),
		),
	)

	// Create a proxy reader to track progress
	srcReader, err := srcFile.Open(ctx)
	if err != nil {
		return fmt.Errorf("failed to open source file for reading: %v", err)
	}
	defer srcReader.Close()

	reader := bar.ProxyReader(srcReader)

	// Get the modTime from the source file (if not available, use time.Now())
	modTime := srcFile.ModTime(ctx)

	// Attempt to get the MD5 hash from the source file.
	md5sum, err := srcFile.Hash(ctx, hash.MD5)
	if err != nil {
		// Not all remotes support hashes so it's ok to leave it empty.
		md5sum = ""
	}

	// Build the hashes map if we got a valid MD5 sum.
	var hashes map[hash.Type]string
	if md5sum != "" {
		hashes = map[hash.Type]string{
			hash.MD5: md5sum,
		}
	}

	// Create an ObjectInfo for the destination file with the dstPath as its remote name
	dstInfo := object.NewStaticObjectInfo(dstPath, modTime, size, false, hashes, dstFs)

	// Copy the file to the destination with progress tracking
	_, err = dstFs.Put(ctx, reader, dstInfo)
	if err != nil {
		return fmt.Errorf("failed to copy file: %v", err)
	}

	// Wait for the progress bar to complete
	p.Wait()

	return nil
}

func main() {
	// Initialize the rclone configuration
	config.SetConfigPath("/etc/rclone.conf")
	ctx := context.Background()

	// Set up the source filesystem (e.g., local filesystem)
	srcFs, err := fs.NewFs(ctx, "/")
	if err != nil {
		log.Fatalf("failed to create source filesystem: %v", err)
	}

	// Set up the destination filesystem (e.g., remote filesystem)
	dstFs, err := fs.NewFs(ctx, "azure:rnd/")
	if err != nil {
		log.Fatalf("failed to create destination filesystem: %v", err)
	}

	// Example usage of copy function
	err = CopyFiles(srcFs, "toto.txt", dstFs, "test/toto.txt")
	if err != nil {
		log.Fatalf("failed to copy files: %v", err)
	}

	// Example usage of list function
	err = ListFiles(dstFs, "test/")
	if err != nil {
		log.Fatalf("failed to list files: %v", err)
	}
}
