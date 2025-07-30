package transformer

import (
	"context"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/fsouza/fake-gcs-server/fakestorage"
	"google.golang.org/api/option"
)

// setupFakeGCS creates a new fake GCS server, a client connected to it,
// and pre-populates it with test objects. It returns the client and a cleanup function.
func setupFakeGCS(t *testing.T, bucketName string, objects []fakestorage.Object) (*storage.Client, func()) {
	// Create a new fake server
	server, err := fakestorage.NewServerWithOptions(fakestorage.Options{
		InitialObjects: objects,
		Host:           "127.0.0.1", // Bind to localhost
	})
	if err != nil {
		t.Fatalf("Failed to create fake GCS server: %v", err)
	}

	// Create a client that connects to the fake server
	client, err := storage.NewClient(context.Background(), option.WithHTTPClient(server.HTTPClient()))
	if err != nil {
		t.Fatalf("Failed to create GCS client for fake server: %v", err)
	}

	// The cleanup function stops the server
	cleanup := func() {
		server.Stop()
	}

	return client, cleanup
}

func TestGCSFileSystem_InitializeAndRead(t *testing.T) {
	// ARRANGE
	bucketName := "test-bucket"
	rootPath := "integrations/v1"

	// Define the objects that will "exist" in our fake GCS bucket
	fakeObjects := []fakestorage.Object{
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: bucketName,
				Name:       "integrations/v1/", // The root directory object
			},
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: bucketName,
				Name:       "integrations/v1/service.yaml",
			},
			Content: []byte("kind: Service"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: bucketName,
				Name:       "integrations/v1/deployment.yaml",
			},
			Content: []byte("kind: Deployment"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: bucketName,
				Name:       "other/folder/ignore.txt", // A file outside our rootPath
			},
			Content: []byte("should be ignored"),
		},
	}

	// Start the fake server and get a client pointing to it
	client, cleanup := setupFakeGCS(t, bucketName, fakeObjects)
	defer cleanup() // Ensure the server is stopped after the test

	// ACT
	// Create our gcsFileSystem. The Initialize() method is called inside the constructor.
	fs, err := newGCSFileSystem(client, bucketName, rootPath)

	// ASSERT
	if err != nil {
		t.Fatalf("newGCSFileSystem() returned an unexpected error: %v", err)
	}

	// 1. Check that the files we expect were downloaded into the in-memory FS
	t.Run("Check Exists", func(t *testing.T) {
		if !fs.Exists("integrations/v1/service.yaml") {
			t.Error("Expected 'integrations/v1/service.yaml' to exist in the memory cache, but it doesn't")
		}
		if !fs.Exists("integrations/v1/deployment.yaml") {
			t.Error("Expected 'integrations/v1/deployment.yaml' to exist in the memory cache, but it doesn't")
		}
		if fs.Exists("other/folder/ignore.txt") {
			t.Error("Expected 'other/folder/ignore.txt' to NOT exist in the memory cache, but it does")
		}
	})

	// 2. Check the content of a downloaded file
	t.Run("Check ReadFile", func(t *testing.T) {
		content, err := fs.ReadFile("integrations/v1/service.yaml")
		if err != nil {
			t.Fatalf("ReadFile() returned an unexpected error: %v", err)
		}
		if string(content) != "kind: Service" {
			t.Errorf("ReadFile() content got = '%s', want 'kind: Service'", string(content))
		}
	})

	// 3. Check that unsupported operations still return the correct error
	t.Run("Check Unsupported Write", func(t *testing.T) {
		err := fs.WriteFile("integrations/v1/new-file.txt", []byte("data"))
		if err != errNotSupported {
			t.Errorf("WriteFile() error got = %v, want %v", err, errNotSupported)
		}
	})
}
