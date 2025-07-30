package transformer

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"testing/fstest"
)

func TestEmbeddedFileSystem_UnsupportedOperations(t *testing.T) {
	efs, err := newEmbeddedFileSystem()
	if err != nil {
		t.Fatalf("newEmbeddedFileSystem() returned an unexpected error: %v", err)
	}

	unsupportedFuncs := map[string]func() error{
		"Create": func() error {
			_, err := efs.Create("some/file.txt")
			return err
		},
		"Mkdir": func() error {
			return efs.Mkdir("some/dir")
		},
		"MkdirAll": func() error {
			return efs.MkdirAll("some/path/to/dir")
		},
		"RemoveAll": func() error {
			return efs.RemoveAll("some/path")
		},
		"WriteFile": func() error {
			return efs.WriteFile("some/file.txt", []byte("data"))
		},
	}

	for name, fn := range unsupportedFuncs {
		t.Run(name, func(t *testing.T) {
			err := fn()
			if err == nil {
				t.Errorf("%s should have returned an error, but got nil", name)
			}
			if err != errNotSupported {
				t.Errorf("%s returned error '%v', want '%v'", name, err, errNotSupported)
			}
		})
	}
}

func TestEmbeddedFileSystem_Walk(t *testing.T) {
	// ARRANGE: Define a mock filesystem
	mockFS := fstest.MapFS{
		".":         {Mode: fs.ModeDir}, // The root is needed for Walk to start
		"a":         {Mode: fs.ModeDir},
		"a/b.txt":   {Data: []byte("b")},
		"a/c":       {Mode: fs.ModeDir},
		"a/c/d.txt": {Data: []byte("d")},
		"e.txt":     {Data: []byte("e")},
	}

	efs := &embeddedFileSystem{FS: mockFS}

	t.Run("successful walk of all files", func(t *testing.T) {
		// ARRANGE
		var visitedPaths []string

		walkFn := func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			visitedPaths = append(visitedPaths, path)
			return nil
		}

		// ACT
		err := efs.Walk(".", walkFn)

		// ASSERT
		if err != nil {
			t.Fatalf("Walk() returned an unexpected error: %v", err)
		}

		expectedPaths := []string{".", "a", "a/b.txt", "a/c", "a/c/d.txt", "e.txt"}
		sort.Strings(visitedPaths) // Sort for deterministic comparison
		sort.Strings(expectedPaths)

		if !reflect.DeepEqual(visitedPaths, expectedPaths) {
			t.Errorf("Walk() visited paths got = %v, want %v", visitedPaths, expectedPaths)
		}
	})

	t.Run("walk function returns an error", func(t *testing.T) {
		// ARRANGE
		var visitedPaths []string
		stopErr := fmt.Errorf("stopping walk")

		walkFn := func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}
			visitedPaths = append(visitedPaths, path)
			// Stop the walk when we see a specific file
			if path == "a/b.txt" {
				return stopErr
			}
			return nil
		}

		// ACT
		err := efs.Walk("a", walkFn) // Start walk from subdir 'a'

		// ASSERT
		if err == nil {
			t.Fatal("Walk() expected an error, but got nil")
		}
		if err != stopErr {
			t.Errorf("Walk() error got = %v, want %v", err, stopErr)
		}

		expectedPaths := []string{"a", "a/b.txt"}
		sort.Strings(visitedPaths) // Sort to ensure order for comparison
		sort.Strings(expectedPaths)

		if !reflect.DeepEqual(visitedPaths, expectedPaths) {
			t.Errorf("Walk() visited paths got = %v, want %v", visitedPaths, expectedPaths)
		}
	})
}

func TestEmbeddedFileSystem_CleanedAbs(t *testing.T) {
	// ARRANGE: Define a mock filesystem
	mockFS := fstest.MapFS{
		"a":         {Mode: fs.ModeDir},
		"a/b.txt":   {Data: []byte("file b")},
		"a/c":       {Mode: fs.ModeDir},
		"a/c/d.txt": {Data: []byte("file d")},
	}

	efs := &embeddedFileSystem{FS: mockFS}

	t.Run("path is a file", func(t *testing.T) {
		// ACT
		dir, file, err := efs.CleanedAbs("a/b.txt")

		// ASSERT
		if err != nil {
			t.Fatalf("CleanedAbs() returned an unexpected error: %v", err)
		}

		expectedDir, _ := filepath.Abs("a")
		if string(dir) != expectedDir {
			t.Errorf("Expected directory part to be '%s', got '%s'", expectedDir, dir)
		}
		if file != "b.txt" {
			t.Errorf("Expected file part to be 'b.txt', got '%s'", file)
		}
	})

	t.Run("path is a directory", func(t *testing.T) {
		// ACT
		dir, file, err := efs.CleanedAbs("a/c")

		// ASSERT
		if err != nil {
			t.Fatalf("CleanedAbs() returned an unexpected error: %v", err)
		}

		expectedDir, _ := filepath.Abs("a/c")
		if string(dir) != expectedDir {
			t.Errorf("Expected directory part to be '%s', got '%s'", expectedDir, dir)
		}
		if file != "" {
			t.Errorf("Expected file part to be empty, got '%s'", file)
		}
	})

	t.Run("path does not exist", func(t *testing.T) {
		// ACT
		_, _, err := efs.CleanedAbs("non/existent/path.txt")

		// ASSERT
		if err == nil {
			t.Error("Expected an error for a non-existent path, but got nil")
		}
	})
}

func TestEmbeddedFileSystem_ReadOperations(t *testing.T) {
	// Define the files for our mock filesystem
	mockFiles := map[string]*fstest.MapFile{
		"hello.txt": {
			Data: []byte("hello world"),
		},
		// --- THIS IS THE CRITICAL PART ---
		// Explicitly define the directory entry itself.
		"dir1": {
			Mode: fs.ModeDir,
		},
		// Now define the files inside the directory
		"dir1/file1.txt": {
			Data: []byte("file one"),
		},
		"dir1/file2.log": {
			Data: []byte("log data"),
		},
		"dir1/subdir": {
			Mode: fs.ModeDir,
		},
	}
	mockFS := fstest.MapFS(mockFiles)

	// ARRANGE: Create an instance of embeddedFileSystem and INJECT our mock filesystem directly.
	// We do NOT use the setupTestFS helper or the newEmbeddedFileSystem() constructor.
	efs := &embeddedFileSystem{FS: mockFS}

	// ACT & ASSERT
	t.Run("Exists", func(t *testing.T) {
		if !efs.Exists("hello.txt") {
			t.Error("Exists() returned false for a file that exists at root")
		}
		if !efs.Exists("dir1/file1.txt") {
			t.Error("Exists() returned false for a file that exists in a dir")
		}
		if !efs.Exists("dir1") {
			t.Error("Exists() returned false for a directory that exists")
		}
		if efs.Exists("nonexistent.file") {
			t.Error("Exists() returned true for a file that does not exist")
		}
	})

	t.Run("ReadFile correctly reads a file", func(t *testing.T) {
		// ACT
		content, err := efs.ReadFile("hello.txt")

		// ASSERT
		if err != nil {
			t.Fatalf("ReadFile() returned an unexpected error: %v", err)
		}
		if string(content) != "hello world" {
			t.Errorf("ReadFile() content got = '%s', want 'hello world'", string(content))
		}
	})

	t.Run("Open and Read correctly opens and reads a file", func(t *testing.T) {
		// ACT
		file, err := efs.Open("dir1/file1.txt")

		// ASSERT
		if err != nil {
			t.Fatalf("Open() returned an unexpected error: %v", err)
		}
		if file == nil {
			t.Fatal("Open() returned a nil file")
		}
		defer file.Close()

		// Read from the opened file to confirm it's correct
		content, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("io.ReadAll() returned an unexpected error: %v", err)
		}
		if string(content) != "file one" {
			t.Errorf("Read() content got = '%s', want 'file one'", string(content))
		}
	})

	t.Run("Open returns an error for non-existent file", func(t *testing.T) {
		// ACT
		_, err := efs.Open("nonexistent.txt")

		// ASSERT
		if err == nil {
			t.Fatal("Open() expected an error for a non-existent file, but got nil")
		}
	})

	t.Run("IsDir", func(t *testing.T) {
		if !efs.IsDir("dir1") {
			t.Error("IsDir() returned false for a directory")
		}
		if efs.IsDir("hello.txt") {
			t.Error("IsDir() returned true for a file")
		}
	})

	t.Run("ReadDir", func(t *testing.T) {
		entries, err := efs.ReadDir("dir1")
		if err != nil {
			t.Fatalf("ReadDir() returned an unexpected error: %v", err)
		}
		expected := []string{"file1.txt", "file2.log", "subdir"}
		sort.Strings(entries)
		sort.Strings(expected)
		if !reflect.DeepEqual(entries, expected) {
			t.Errorf("ReadDir() got = %v, want %v", entries, expected)
		}
	})

	t.Run("Glob", func(t *testing.T) {
		matches, err := efs.Glob("dir1/*.txt")
		if err != nil {
			t.Fatalf("Glob() returned an unexpected error: %v", err)
		}
		expected := []string{"dir1/file1.txt"}
		if !reflect.DeepEqual(matches, expected) {
			t.Errorf("Glob() got = %v, want %v", matches, expected)
		}
	})
}
