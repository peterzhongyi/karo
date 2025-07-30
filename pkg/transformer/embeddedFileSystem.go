package transformer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/GoogleCloudPlatform/karo/assets"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

var (
	errNotSupported = errors.New("not supported")
)

type embeddedFileSystem struct {
	fs.FS
}

var _ filesys.FileSystem = (*embeddedFileSystem)(nil)

// newEmbeddedFileSystem creates a new embedded file system.
func newEmbeddedFileSystem() (filesys.FileSystem, error) {
	return &embeddedFileSystem{FS: assets.Embedded}, nil

}

// Create a file.
func (f *embeddedFileSystem) Create(path string) (filesys.File, error) {
	return nil, errNotSupported
}

// MkDir makes a directory.
func (f *embeddedFileSystem) Mkdir(path string) error {
	return errNotSupported
}

// MkDirAll makes a directory path, creating intervening directories.
func (f *embeddedFileSystem) MkdirAll(path string) error {
	return errNotSupported
}

// RemoveAll removes path and any children it contains.
func (f *embeddedFileSystem) RemoveAll(path string) error {
	return errNotSupported
}

// Open opens the named file for reading.
func (f *embeddedFileSystem) Open(path string) (filesys.File, error) {
	file, err := f.FS.Open(path)
	if err != nil {
		return nil, err
	}
	return &embeddedFile{file}, nil
}

// IsDir returns true if the path is a directory.
func (f *embeddedFileSystem) IsDir(path string) bool {
	info, err := fs.Stat(f.FS, path)

	if err != nil {
		return false
	}
	return info.IsDir()
}

// ReadDir returns a list of files and directories within a directory.
func (f *embeddedFileSystem) ReadDir(path string) ([]string, error) {
	dirEntries, err := fs.ReadDir(f.FS, path)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(dirEntries))
	for i := range dirEntries {
		result[i] = dirEntries[i].Name()
	}
	return result, nil
}

// CleanedAbs converts the given path into a
// directory and a file name, where the directory
// is represented as a ConfirmedDir and all that implies.
// If the entire path is a directory, the file component
// is an empty string.
func (f *embeddedFileSystem) CleanedAbs(path string) (filesys.ConfirmedDir, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	info, err := fs.Stat(f.FS, path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return filesys.ConfirmedDir(abs), "", nil
	}
	return filesys.ConfirmedDir(filepath.Dir(abs)), filepath.Base(abs), nil
}

// Exists is true if the path exists in the file system.
func (f *embeddedFileSystem) Exists(path string) bool {
	_, err := fs.Stat(f.FS, path)
	return err == nil
}

// Glob returns the list of matching files,
// emulating https://golang.org/pkg/path/filepath/#Glob
func (f *embeddedFileSystem) Glob(pattern string) ([]string, error) {
	return fs.Glob(f.FS, pattern)
}

// ReadFile returns the contents of the file at the given path.
func (f *embeddedFileSystem) ReadFile(path string) ([]byte, error) {
	return fs.ReadFile(f.FS, path)
}

// WriteFile writes the data to a file at the given path,
// overwriting anything that's already there.
func (f *embeddedFileSystem) WriteFile(path string, data []byte) error {
	return errNotSupported
}

// Walk walks the file system with the given WalkFunc.
func (f *embeddedFileSystem) Walk(path string, walkFn filepath.WalkFunc) error {
	return fs.WalkDir(f.FS, path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := fs.Stat(f.FS, path)
		if err != nil {
			return err
		}
		return walkFn(path, info, err)
	})
}

type embeddedFile struct {
	file fs.File
}

var _ filesys.File = (*embeddedFile)(nil)

func (f *embeddedFile) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

func (f *embeddedFile) Write(p []byte) (n int, err error) {
	return 0, errNotSupported
}

func (f *embeddedFile) Close() error {
	return f.file.Close()
}

func (f *embeddedFile) Stat() (os.FileInfo, error) {
	return f.file.Stat()
}
