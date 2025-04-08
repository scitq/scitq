package fetch

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	//"net/url"

	//"strings"
	"time"

	"github.com/rclone/rclone/fs/config/configfile"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/walk"

	//"github.com/rclone/rclone/fs/config/registry"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/object"
	"github.com/rclone/rclone/fs/operations"

	"github.com/google/uuid"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/rc"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	_ "github.com/rclone/rclone/backend/all" // Ensure all backends are loaded
)

//func CopyFiles(srcFs fs.Fs, srcPath string, dstFs fs.Fs, dstPath string) error {
//	// Create a context for the operation
//	ctx := context.Background()
//	// Copy the file
//	err := operations.CopyFile(ctx, dstFs, srcFs, dstPath, srcPath)
//	if err != nil {
//		return fmt.Errorf("failed to copy file: %v", err)
//	}
//
//	return nil
//}

//func ListFiles(fsys fs.Fs, path string) error {
//	// Create a context for the operation
//	ctx := context.Background()
//
//	// List the files in the directory
//	dir, err := fsys.List(ctx, path)
//	if err != nil {
//		return fmt.Errorf("failed to list files: %v", err)
//	}
//
//	// Print the file names
//	for _, entry := range dir {
//		fmt.Println(entry.Remote())
//	}
//
//	return nil
//}

const DefaultRcloneConfig = "/etc/rclone.conf"

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

//func RecursiveCopy(srcFs fs.Fs, srcPath string, dstFs fs.Fs, dstPath string) error {
//	ctx := context.Background()
//
//	// List the contents of the source directory
//	entries, err := srcFs.List(ctx, srcPath)
//	if err != nil {
//		return fmt.Errorf("failed to list source directory: %v", err)
//	}
//
//	// Iterate over each entry in the source directory
//	for _, entry := range entries {
//		srcEntryPath := entry.Remote()
//		dstEntryPath := dstPath + "/" + entry.Remote()
//
//		// Check if the entry is a directory
//		if _, ok := entry.(fs.Directory); ok {
//			// Create the directory in the destination
//			err := dstFs.Mkdir(ctx, dstEntryPath)
//			if err != nil {
//				return fmt.Errorf("failed to create directory: %v", err)
//			}
//
//			// Recursively copy the contents of the directory
//			err = RecursiveCopy(srcFs, srcEntryPath, dstFs, dstEntryPath)
//			if err != nil {
//				return fmt.Errorf("failed to recursively copy directory: %v", err)
//			}
//		} else {
//			// Copy the file
//			err := CopyFilesWithProgress(srcFs, srcEntryPath, dstFs, dstEntryPath)
//			if err != nil {
//				return fmt.Errorf("failed to copy file: %v", err)
//			}
//		}
//	}
//
//	return nil
//}

// FetchContext holds global configuration settings for the fetch package.
var RcloneRemotes []string

// stringInSlice checks if a string is present in a slice of strings.
func stringInSlice(target string, slice []string) bool {
	for _, element := range slice {
		if element == target {
			return true
		}
	}
	return false
}

// FileSystemInterface defines the methods for file operations.
type FileSystemInterface interface {
	Copy(otherFs FileSystemInterface, src, dst URI, isSelfSource bool) error
	List(path string) (fs.DirEntries, error)
	Mkdir(path string) error
	Info(path string) (fs.DirEntry, error)
}

// MetaFileSystem provides common functionality for file operations using any FileSystemInterface.
type MetaFileSystem struct {
	fs FileSystemInterface
}

// NewMetaFileSystem creates a new MetaFileSystem with the given FileSystemInterface.
func NewMetaFileSystem(fs FileSystemInterface, err error) (*MetaFileSystem, error) {
	if err != nil {
		return nil, err
	} else {
		return &MetaFileSystem{fs: fs}, nil
	}
}

// RecursiveCopy performs a recursive copy using the embedded FileSystemInterface.
func (mfs *MetaFileSystem) RecursiveCopy(otherFs MetaFileSystem, src, dst URI, isSelfSource bool) error {
	entries, err := mfs.fs.List(src.Path)
	if err != nil {
		return fmt.Errorf("failed to list source directory: %v", err)
	}
	for _, entry := range entries {
		srcEntry := src.Subpath(entry.Remote())
		dstEntry := dst.Subpath(dst.Path + "/" + entry.Remote())
		if _, ok := entry.(fs.Directory); ok {
			err := mfs.fs.Mkdir(dstEntry.Path)
			if err != nil {
				return fmt.Errorf("failed to create directory: %v", err)
			}
			err = mfs.RecursiveCopy(otherFs, srcEntry, dstEntry, isSelfSource)
			if err != nil {
				return fmt.Errorf("failed to recursively copy directory: %v", err)
			}
		} else {
			err := mfs.fs.Copy(otherFs.fs, srcEntry, dstEntry, isSelfSource)
			if err != nil {
				return fmt.Errorf("failed to copy file: %v", err)
			}
		}
	}
	return nil
}

// RcloneBackend implements the FileSystemInterface using rclone.
type RcloneBackend struct {
	rcloneFs fs.Fs
}

// NewRcloneBackend creates a new RcloneBackend.
func NewRcloneBackend(ctx context.Context, remote string) (*RcloneBackend, error) {
	rcloneFs, err := fs.NewFs(ctx, remote)
	if err != nil {
		return nil, fmt.Errorf("failed to create rclone filesystem: %v", err)
	}
	return &RcloneBackend{rcloneFs: rcloneFs}, nil
}

// Copy implements the Copy method for RcloneBackend.
func (rb *RcloneBackend) Copy(otherFsInterface FileSystemInterface, src, dst URI, selfIsSource bool) error {
	var otherFs *RcloneBackend // Pointer to hold the concrete type

	if src.File == "" {
		return fmt.Errorf("cannot copy directory: source %s", src)
	}

	switch v := otherFsInterface.(type) {
	case *RcloneBackend:
		otherFs = v // Correct type assertion
	case *LocalBackend:
		var localFs *LocalBackend
		localFs = v
		otherFs = &localFs.RcloneBackend
	default:
		return fmt.Errorf("Copy of RcloneBackend only supports RcloneBackend or LocalBackend, not %T", v)
	}

	ctx := context.Background()
	var err error
	if selfIsSource {
		//log.Printf("Copy <%s>:<%s> -> <%s>:<%s>", rb.rcloneFs.Name()+":"+rb.rcloneFs.Root(), src.CompletePath(), otherFs.rcloneFs.Name()+":"+otherFs.rcloneFs.Root(), dst.CompletePath())
		err = operations.CopyFile(ctx, otherFs.rcloneFs, rb.rcloneFs, dst.CompletePath(), src.CompletePath())
	} else {
		//log.Printf("Copy <%s>:<%s> -> <%s>:<%s>", otherFs.rcloneFs.Name()+":"+otherFs.rcloneFs.Root(), src.CompletePath(), rb.rcloneFs.Name()+":"+rb.rcloneFs.Root(), dst.CompletePath())
		err = operations.CopyFile(ctx, rb.rcloneFs, otherFs.rcloneFs, dst.CompletePath(), src.CompletePath())
	}
	if err != nil {
		return fmt.Errorf("failed to copy file: %v", err)
	}
	return nil
}

// List implements the List method for RcloneBackend.
func (rb *RcloneBackend) List(path string) (fs.DirEntries, error) {
	ctx := context.Background()

	var entries fs.DirEntries
	err := walk.ListR(ctx, rb.rcloneFs, path, true, 0, walk.ListAll, func(newEntries fs.DirEntries) error {
		entries = append(entries, newEntries...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %v", err)
	}
	return entries, nil
}

// Mkdir implements the Mkdir method for RcloneBackend.
func (rb *RcloneBackend) Mkdir(path string) error {
	ctx := context.Background()
	return rb.rcloneFs.Mkdir(ctx, path)
}

func (rb *RcloneBackend) Info(path string) (fs.DirEntry, error) {
	ctx := context.Background()
	obj, err := rb.rcloneFs.NewObject(ctx, path)
	if err == nil {
		return obj, nil
	}

	// Check if the error is because `path` is a directory
	if err == fs.ErrorIsDir {
		// Return a manually created directory entry
		dir := fs.NewDir(path, time.Now()) // Use current time as placeholder
		return dir, nil
	}

	return nil, err // Return the original error if it's not a directory issue
}

// LocalBackend implements a local FileSystemInterface using rclone but is also capable of working with other backends
type LocalBackend struct {
	RcloneBackend
	localPath string
	isLocal   bool
}

// NewLocalBackend creates a new LocalBackend.
func NewLocalBackend(ctx context.Context, component string) (*LocalBackend, error) {
	rcloneFs, err := fs.NewFs(ctx, component)
	if err != nil {
		return nil, fmt.Errorf("failed to create rclone filesystem: %v", err)
	}
	rb := RcloneBackend{rcloneFs: rcloneFs}
	return &LocalBackend{RcloneBackend: rb, localPath: component, isLocal: component == "./" || component == "../" || component == ".\\" || component == "..\\"}, nil
}

func (lb LocalBackend) AbsolutePath(path string) (string, error) {
	if lb.isLocal {
		absolutePath, err := filepath.Abs(lb.localPath)
		if err != nil {
			return "", fmt.Errorf("could not get absolute path of %s: %w", lb.localPath, err)
		}
		return filepath.Join(absolutePath, path), nil
	} else {
		return lb.rcloneFs.Root() + path, nil
	}
}

type Operation struct {
	src    *MetaFileSystem
	dst    *MetaFileSystem
	srcUri URI
	dstUri URI
}

// Load Rclone config from a file into memory
func loadConfigFromFile(configPath string) {
	config.SetConfigPath(configPath)
	configfile.Install()

	// Clone the loaded config into memory
	cfg := config.Data()
	tempConfig := make(map[string]map[string]string)
	if cfg != nil {
		// Convert to a temporary in-memory config
		for _, remote := range cfg.GetSectionList() {
			options := make(map[string]string)
			for _, k := range cfg.GetKeyList(remote) {
				options[k], _ = cfg.GetValue(remote, k)
			}
			tempConfig[remote] = options
		}
	}

	tmpFile, err := os.CreateTemp("", "rclone-config-*.conf")
	if err != nil {
		log.Fatalf("failed to create temp file: %v", err)
	}
	//defer os.Remove(tmpFile.Name())

	//config.SetData(cfg)
	//configfile.Install()
	//config.Data().Save()

	// Write the cloned config to the temporary file
	for name, options := range tempConfig {
		fmt.Fprintf(tmpFile, "[%s]\n", name)
		for k, v := range options {
			fmt.Fprintf(tmpFile, "%s = %s\n", k, v)
		}
	}
	tmpFile.Close()

	// Set the fake config path
	config.SetConfigPath(tmpFile.Name())
	configfile.Install()

	RcloneRemotes = config.GetRemoteNames()
	log.Printf("Rclone config loaded from %s [remotes %s]", configPath, RcloneRemotes)
}

// Add a new remote in memory without modifying the config file
func addRemoteInMemory(protocol string, options map[string]string) (string, error) {
	uniqueName := protocol + "-" + uuid.New().String()

	cfg := configmap.Simple{}
	for k, v := range options {
		cfg[k] = v
	}

	ctx := context.Background()
	params := rc.Params{}
	for k, v := range cfg {
		params[k] = v
	}

	_, err := config.CreateRemote(ctx, uniqueName, protocol, params, config.UpdateRemoteOpt{})
	if err != nil {
		return "", fmt.Errorf("failed to register remote: %v", err)
	}

	//fmt.Println("Remote", uniqueName, "added in memory!")
	return uniqueName, nil
}

func CleanConfig() {
	configTemp := config.GetConfigPath()
	fmt.Printf("Cleaning temporary conf %s\n", configTemp)
	os.Remove(configTemp)
}

func NewOperation(rcloneConfig, srcStr, dstStr string) (*Operation, error) {
	loadConfigFromFile(rcloneConfig)

	src, err := ParseURI(srcStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source URI: %v", err)
	}
	srcFs, err := src.fs()
	if err != nil {
		return nil, fmt.Errorf("failed to get source FS: %v", err)
	}

	if dstStr == "" {
		return &Operation{src: srcFs, srcUri: *src}, nil
	}

	dst, err := ParseURI(dstStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse destination URI: %v", err)
	}
	dstFs, err := dst.fs()
	if err != nil {
		return nil, fmt.Errorf("failed to get destination FS: %v", err)
	}

	return &Operation{
		src:    srcFs,
		dst:    dstFs,
		srcUri: *src,
		dstUri: *dst,
	}, nil
}

func (op *Operation) Copy() error {
	var err error
	if op.dst == nil {
		return fmt.Errorf("cannot copy with no destination (source %s)", op.srcUri)
	}
	if len(op.dstUri.Actions) > 0 {
		return fmt.Errorf("actions for destination are declared on source for now, cannot handle destination action: %s", op.dstUri)
	}
	if op.dstUri.File == "" {
		op.dstUri.File = op.srcUri.File
	}

	for _, action := range op.dstUri.Actions {
		err := performAction(action, &op.dstUri, op.dst, true)
		if err != nil {
			return fmt.Errorf("early action %s failed in URI %s with error: %s", action, op.dstUri, err)
		}
	}

	switch v := op.src.fs.(type) {
	case *RcloneBackend:
		switch w := op.dst.fs.(type) {
		case *RcloneBackend, *LocalBackend:
			err = v.Copy(w, op.srcUri, op.dstUri, true)
		default:
			return fmt.Errorf("RcloneBackend only support operations with RcloneBackend or LocalBackend, not %T", v)
		}
	case *AsperaBackend:
		switch w := op.dst.fs.(type) {
		case *LocalBackend:
			err = v.Copy(w, op.srcUri, op.dstUri, true)
		default:
			return fmt.Errorf("AsperaBackend only support copy to LocalBackend, not %T", w)
		}
	case *FastqBackend:
		err = v.Copy(op.dst.fs, op.srcUri, op.dstUri, true)
	case *LocalBackend:
		switch w := op.dst.fs.(type) {
		case *RcloneBackend:
			err = w.Copy(&v.RcloneBackend, op.srcUri, op.dstUri, false)
		case *LocalBackend:
			err = v.Copy(&w.RcloneBackend, op.srcUri, op.dstUri, true)
		default:
			err = w.Copy(v, op.srcUri, op.dstUri, false)
		}

	default:
		return fmt.Errorf("unsupported source type: %T", v)
	}

	if err != nil {
		return fmt.Errorf("failed to copy %s -> %s: %v", op.srcUri, op.dstUri, err)
	}

	for _, action := range op.srcUri.Actions {
		err := performAction(action, &op.dstUri, op.dst, false)
		if err != nil {
			return fmt.Errorf("late action %s failed in URI %s with error: %s", action, op.dstUri, err)
		}
	}

	return nil
}

func (op *Operation) List() (fs.DirEntries, error) {
	path := op.srcUri.Path
	if op.srcUri.File != "" {
		if path == "" {
			path = op.srcUri.File
		} else {
			path = path + op.srcUri.Separator + op.srcUri.File
		}
	}
	//log.Printf("Path <%s> | File <%s> -> <%s>\n", op.srcUri.Path, op.srcUri.File, path)

	return op.src.fs.List(path)
}

func (op *Operation) Info() (fs.DirEntry, error) {
	path := op.srcUri.Path
	if op.srcUri.File != "" {
		if path == "" {
			path = op.srcUri.File
		} else {
			path = path + op.srcUri.Separator + op.srcUri.File
		}
	}
	//log.Printf("Path <%s> | File <%s> -> <%s>\n", op.srcUri.Path, op.srcUri.File, path)

	return op.src.fs.Info(path)
}

func Copy(rcloneConfig, srcStr, dstStr string) error {
	op, err := NewOperation(rcloneConfig, srcStr, dstStr)
	defer CleanConfig()
	if err != nil {
		log.Fatalf("Could not initiate copy operation %v", err)
	}
	err = op.Copy()
	return err
}

func List(rcloneConfig, srcStr string) (fs.DirEntries, error) {
	op, err := NewOperation(rcloneConfig, srcStr, "")
	defer CleanConfig()
	if err != nil {
		log.Fatalf("Could not initiate list operation %v", err)
	}
	return op.List()
}

func Info(rcloneConfig, srcStr string) (fs.DirEntry, error) {
	op, err := NewOperation(rcloneConfig, srcStr, "")
	defer CleanConfig()
	if err != nil {
		log.Fatalf("Could not initiate info operation %v", err)
	}
	return op.Info()
}

// test if a DirEntry is a dir
func IsDir(f fs.DirEntry) bool {
	_, ok := f.(fs.Directory)
	return ok
}

// test if a DirEntry is a file
func IsFile(f fs.DirEntry) bool {
	_, ok := f.(fs.Object)
	return ok
}

// provide MD5
func GetMD5(f fs.DirEntry) string {
	ctx := context.Background()

	var md5sum string
	var err error
	if obj, ok := f.(fs.Object); ok {
		md5sum, err = obj.Hash(ctx, hash.MD5)
		if err != nil {
			// Not all remotes support hashes so it's ok to leave it empty.
			md5sum = ""
		}
	}

	return md5sum

}
