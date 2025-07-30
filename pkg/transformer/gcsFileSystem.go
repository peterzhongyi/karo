package transformer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

var errFileNotFound = errors.New("file not found")

type gcsFileSystem struct {
	client       *storage.Client
	bucket       string
	rootPath     string
	memoryFS     filesys.FileSystem
	emptyFolders map[string]bool // Track empty folders using a map for efficient lookups
}

var _ filesys.FileSystem = (*gcsFileSystem)(nil)

// newGCSFileSystem creates a new GCS file system.
func newGCSFileSystem(client *storage.Client, bucket, rootPath string) (filesys.FileSystem, error) {
	fs := &gcsFileSystem{
		client:       client,
		bucket:       bucket,
		rootPath:     rootPath,
		memoryFS:     filesys.MakeFsInMemory(),
		emptyFolders: make(map[string]bool), // Initialize the map
	}
	err := fs.Initialize(context.Background())
	if err != nil {
		return nil, err
	}
	return fs, nil
}

func (f *gcsFileSystem) Initialize(ctx context.Context) error {
	sourceRootPath := fmt.Sprintf("%s/", strings.Trim(f.rootPath, "/"))
	bucket := f.client.Bucket(f.bucket)

	// Check if the bucket exists
	_, err := bucket.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotExist) {
			return fmt.Errorf("bucket %s not found: %w", f.bucket, err)
		}
		return fmt.Errorf("failed to get bucket attributes: %w", err)
	}

	it := bucket.Objects(ctx, &storage.Query{
		Prefix: sourceRootPath,
	})

	// Create the "integrations" directory upfront
	if err := f.memoryFS.MkdirAll(sourceRootPath); err != nil {
		return fmt.Errorf("failed to create %q directory: %w", sourceRootPath, err)
	}

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to iterate GCS objects: %w", err)
		}

		relativePath, err := filepath.Rel(sourceRootPath, attrs.Name)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", attrs.Name, err)
		}

		if relativePath == "." { //skip source folder itself.
			continue
		}

		targetPath := filepath.Join(sourceRootPath, relativePath)
		targetDir := filepath.Dir(targetPath)
		if err := f.memoryFS.MkdirAll(targetDir); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
		}

		if attrs.Size == 0 && strings.HasSuffix(attrs.Name, "/") {
			// Create empty folder in memoryFS
			if err := f.memoryFS.MkdirAll(targetPath); err != nil {
				return fmt.Errorf("failed to create empty directory %s: %w", targetPath, err)
			}
			f.emptyFolders[targetPath] = true // Store the empty folder
			continue
		}

		reader, err := bucket.Object(attrs.Name).NewReader(ctx)
		if err != nil {
			return fmt.Errorf("failed to create GCS object reader for %s: %w", attrs.Name, err)
		}
		defer reader.Close()

		writer, err := f.memoryFS.Create(targetPath)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", targetPath, err)
		}
		defer writer.Close()
		bytesCopied, err := io.Copy(writer, reader)
		if err != nil {
			return fmt.Errorf("failed to copy GCS object %s to %s: %w", attrs.Name, targetPath, err)
		}

		fmt.Println("[DEBUG] Copied bytes", "bytes", bytesCopied, "path", targetPath) //Add this log
	}
	return nil
}

// Create a file.
func (f *gcsFileSystem) Create(path string) (filesys.File, error) {
	return nil, errNotSupported
}

// MkDir makes a directory.
func (f *gcsFileSystem) Mkdir(path string) error {
	return errNotSupported
}

// MkDirAll makes a directory path, creating intervening directories.
func (f *gcsFileSystem) MkdirAll(path string) error {
	return errNotSupported
}

// RemoveAll removes path and any children it contains.
func (f *gcsFileSystem) RemoveAll(path string) error {
	return errNotSupported
}

// Open opens the named file for reading.
func (f *gcsFileSystem) Open(path string) (filesys.File, error) {
	return f.memoryFS.Open(path)
}

// IsDir returns true if the path is a directory.
func (f *gcsFileSystem) IsDir(path string) bool {
	if f.emptyFolders[path] {
		return true
	}
	if !f.memoryFS.Exists(path) {
		return false
	}
	return f.memoryFS.IsDir(path)
}

// ReadDir returns a list of files and directories within a directory.
func (f *gcsFileSystem) ReadDir(path string) ([]string, error) {
	if f.emptyFolders[path] {
		return []string{}, nil
	}
	if !f.memoryFS.Exists(path) {
		return nil, fmt.Errorf("directory '%s' not found", path)
	}
	entries, err := f.memoryFS.ReadDir(path)
	if err != nil {
		return nil, err
	}
	// Add empty folders that are direct children of the path
	for folder := range f.emptyFolders {
		if filepath.Dir(folder) == path {
			base := filepath.Base(folder)
			found := false
			for _, entry := range entries {
				if entry == base {
					found = true
					break
				}
			}
			if !found {
				entries = append(entries, base)
			}
		}
	}
	return entries, nil
}

func (f *gcsFileSystem) isDirectChildOfEmptyFolder(path string) bool {
	for folder := range f.emptyFolders {
		if folder == path {
			return true
		}
	}
	return false
}

// CleanedAbs converts the given path into a
// directory and a file name, where the directory
// is represented as a ConfirmedDir and all that implies.
// If the entire path is a directory, the file component
// is an empty string.
func (f *gcsFileSystem) CleanedAbs(path string) (filesys.ConfirmedDir, string, error) {
	return f.memoryFS.CleanedAbs(path)
}

// Exists is true if the path exists in the file system.
func (f *gcsFileSystem) Exists(path string) bool {
	if f.memoryFS.Exists(path) {
		return true
	}
	return f.emptyFolders[path]
}

// Glob returns the list of matching files,
// emulating https://golang.org/pkg/path/filepath/#Glob
func (f *gcsFileSystem) Glob(pattern string) ([]string, error) {
	return f.memoryFS.Glob(pattern)
}

// ReadFile returns the contents of the file at the given path.
func (f *gcsFileSystem) ReadFile(path string) ([]byte, error) {
	if !f.memoryFS.Exists(path) {
		return nil, errFileNotFound
	}
	return f.memoryFS.ReadFile(path)
}

// WriteFile writes the data to a file at the given path,
// overwriting anything that's already there.
func (f *gcsFileSystem) WriteFile(path string, data []byte) error {
	return errNotSupported
}

// Walk walks the file system with the given WalkFunc.
func (f *gcsFileSystem) Walk(path string, walkFn filepath.WalkFunc) error {
	return f.memoryFS.Walk(path, walkFn)
}
