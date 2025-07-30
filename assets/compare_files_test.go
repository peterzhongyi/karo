package assets

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing" // For a test function

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

const (
	gcsBucket     = "skippy-kustomization-templates"
	gcsPrefix     = "integrations/" // Note the trailing slash for directory-like behavior
	localBasePath = "v1/"
)

// calculateMD5 reads a file and returns its MD5 hash as a hex string.
func calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate MD5 for %s: %w", filePath, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// getLocalFiles retrieves a map of relative paths to their MD5 hashes for local files.
func getLocalFiles(basePath string) (map[string]string, error) {
	localFiles := make(map[string]string)
	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil // Skip directories
		}

		relativePath, err := filepath.Rel(basePath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}
		relativePath = filepath.ToSlash(relativePath) // Normalize path separators

		md5Hash, err := calculateMD5(path)
		if err != nil {
			return fmt.Errorf("failed to calculate MD5 for local file %s: %w", path, err)
		}
		localFiles[relativePath] = md5Hash
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking local directory %s: %w", basePath, err)
	}
	return localFiles, nil
}

// getGCSFiles retrieves a map of relative paths to their MD5 hashes for GCS objects.
func getGCSFiles(ctx context.Context, client *storage.Client, bucketName, prefix string) (map[string]string, error) {
	gcsFiles := make(map[string]string)
	it := client.Bucket(bucketName).Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate GCS objects: %w", err)
		}

		if attrs.Size == 0 && strings.HasSuffix(attrs.Name, "/") {
			// This is likely a simulated directory object. Skip.
			continue
		}

		// GCS MD5 hash is base64 encoded, but we need hex for comparison with local MD5.
		// The MD5 hash provided by GCS for objects is already the hex representation of the hash.
		// It's typically available in attrs.MD5.
		// If attrs.MD5 is not populated or is base64, you would need to decode it.
		// For Kustomize YAMLs, GCS typically provides the hex MD5 in attrs.MD5.

		// Ensure MD5 is present. If not, you might need to download and calculate.
		if attrs.MD5 == nil {
			log.Printf("Warning: GCS object %s has no MD5 hash. Will skip content comparison for this file.", attrs.Name)
			continue
		}

		relativePath := strings.TrimPrefix(attrs.Name, prefix)
		gcsFiles[relativePath] = hex.EncodeToString(attrs.MD5) // GCS MD5 is byte slice, convert to hex string
	}
	return gcsFiles, nil
}

// TestDirectorySimilarity compares a local directory with a GCS bucket path.
func TestDirectorySimilarity(t *testing.T) {
	ctx := context.Background()

	// Initialize GCS client
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("Failed to create GCS client: %v", err)
	}
	defer client.Close()

	// Get local file information
	localFiles, err := getLocalFiles(localBasePath)
	if err != nil {
		t.Fatalf("Failed to get local files: %v", err)
	}
	t.Logf("Found %d local files.", len(localFiles))

	// Get GCS file information
	gcsFiles, err := getGCSFiles(ctx, client, gcsBucket, gcsPrefix)
	if err != nil {
		t.Fatalf("Failed to get GCS files: %v", err)
	}
	t.Logf("Found %d GCS files in bucket %s with prefix %s.", len(gcsFiles), gcsBucket, gcsPrefix)

	// Compare file lists (keys in the maps)
	localPaths := make([]string, 0, len(localFiles))
	for path := range localFiles {
		localPaths = append(localPaths, path)
	}
	sort.Strings(localPaths)

	gcsPaths := make([]string, 0, len(gcsFiles))
	for path := range gcsFiles {
		gcsPaths = append(gcsPaths, path)
	}
	sort.Strings(gcsPaths)

	// Check for missing/extra files
	var missingLocal, missingGCS []string

	// Compare local to GCS
	for _, localPath := range localPaths {
		if _, exists := gcsFiles[localPath]; !exists {
			missingGCS = append(missingGCS, localPath)
		}
	}

	// Compare GCS to local
	for _, gcsPath := range gcsPaths {
		if _, exists := localFiles[gcsPath]; !exists {
			missingLocal = append(missingLocal, gcsPath)
		}
	}

	if len(missingGCS) > 0 {
		t.Errorf("Files present locally but missing in GCS: %v", missingGCS)
	}
	if len(missingLocal) > 0 {
		t.Errorf("Files present in GCS but missing locally: %v", missingLocal)
	}

	// Compare file contents (MD5 hashes) for common files
	var contentMismatches []string
	for localPath, localMD5 := range localFiles {
		if gcsMD5, exists := gcsFiles[localPath]; exists {
			if localMD5 != gcsMD5 {
				contentMismatches = append(contentMismatches, fmt.Sprintf("File %s: Local MD5 %s != GCS MD5 %s", localPath, localMD5, gcsMD5))
			}
		}
	}

	if len(contentMismatches) > 0 {
		t.Errorf("Content mismatches found:\n%s", strings.Join(contentMismatches, "\n"))
	}

	if len(missingGCS) == 0 && len(missingLocal) == 0 && len(contentMismatches) == 0 {
		t.Log("Local and GCS directories are similar (same files and content).")
	} else {
		t.Errorf("Directory similarity check failed.")
	}
}

func main() {
	// This main function is just for demonstration if you want to run it as a standalone program.
	// For a proper Go test, you'd put the TestDirectorySimilarity function in a file ending with _test.go
	// and run it with `go test`.

	// To run this main function, uncomment the following:
	// t := &testing.T{}
	// TestDirectorySimilarity(t)
	// if t.Failed() {
	// 	os.Exit(1)
	// }
	// fmt.Println("All tests passed!")

	// Example of running the test directly for debugging purposes:
	// Set up dummy directories for testing without GCS access:
	// Create a temporary directory for local assets
	tmpDir, err := os.MkdirTemp("", "test_assets_v1")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Simulate local assets content
	localFilePath1 := filepath.Join(tmpDir, "apply", "apply.yaml")
	localFilePath2 := filepath.Join(tmpDir, "endpoint", "base", "deployment.yaml")
	os.MkdirAll(filepath.Dir(localFilePath1), 0755)
	os.MkdirAll(filepath.Dir(localFilePath2), 0755)
	os.WriteFile(localFilePath1, []byte("local apply content"), 0644)
	os.WriteFile(localFilePath2, []byte("local deployment content"), 0644)

	// Override localBasePath for the test
	// Note: In a real `go test` environment, you'd want to pass this as an argument
	// or use a more sophisticated test setup.
	// For this example, we'll just comment out the `TestDirectorySimilarity` call
	// and demonstrate the helper functions if you want to run them in main.

	// To actually run TestDirectorySimilarity with GCS, you'll need to ensure
	// your environment is authenticated (e.g., `gcloud auth application-default login`)
	// and the bucket exists with the specified prefix.
}
