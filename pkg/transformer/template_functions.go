package transformer

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

func extractValueAfterEquals(args []interface{}, key string) (string, error) {
	keyWithEquals := key + "="

	for _, arg := range args {
		input := fmt.Sprintf("%v", arg)
		if strings.HasPrefix(input, keyWithEquals) {
			value := strings.TrimPrefix(input, keyWithEquals)
			value = strings.TrimSpace(value)
			// Also treat a key with an empty value as "not found" for the 'or' pipeline.
			if value == "" {
				return "", nil
			}
			return value, nil
		}
	}

	// Key was not found. Instead of erroring, return an empty string and no error.
	// This allows the template's `or` function to work correctly.
	return "", nil
}

func minPerformanceAccelerator(options []interface{}) map[string]interface{} {
	if len(options) == 0 {
		return nil
	}

	minPerformance := math.MaxFloat64
	var bestOption map[string]interface{}
	performanceKey := "outputTokensPerSecond" // Or "queriesPerSecond"

	for _, option := range options {
		if opt, ok := option.(map[string]interface{}); ok {
			if acceleratorType, ok := opt["acceleratorType"].(string); ok && strings.HasPrefix(acceleratorType, "nvidia-") {
				if stats, ok := opt["performanceStats"].(map[string]interface{}); ok {
					if performance, ok := stats[performanceKey].(float64); ok {
						if performance < minPerformance {
							minPerformance = performance
							bestOption = opt
						}
					} else if performanceInt, ok := stats[performanceKey].(int); ok {
						performanceFloat := float64(performanceInt)
						if performanceFloat < minPerformance {
							minPerformance = performanceFloat
							bestOption = opt
						}
					}
				}
			}
		}
	}
	if bestOption == nil {
		return nil // Return nil if no suitable option was found
	}
	return bestOption

}

// _parseGCSURI is a private helper that runs the regex once. It gracefully handles
// inputs that are not valid GCS URIs by returning empty values and no error.
func _parseGCSURI(gcsURI string) (map[string]string, error) {
	trimmedURI := strings.TrimSpace(gcsURI)
	// If the input is empty or not a GCS path, don't treat it as an error.
	// Return empty results so the template can handle it gracefully.
	if trimmedURI == "" || !strings.HasPrefix(trimmedURI, "gs://") {
		return make(map[string]string), nil
	}

	match := gcsRegex.FindStringSubmatch(trimmedURI)
	if match == nil {
		// This case is now only for malformed gs:// URIs, e.g., "gs:// "
		return nil, fmt.Errorf("invalid GCS URI format: %s", gcsURI)
	}

	results := make(map[string]string)
	for i, name := range gcsRegex.SubexpNames() {
		if i != 0 && name != "" {
			results[name] = match[i]
		}
	}
	return results, nil
}

// getGcsBucketFromURI calls the private parser and returns the bucket part.
func getGcsBucketFromURI(gcsURI string) (string, error) {
	parts, err := _parseGCSURI(gcsURI)
	if err != nil {
		return "", err
	}
	return parts["bucket"], nil
}

// getGcsPathFromURI calls the private parser and returns the path part.
func getGcsPathFromURI(gcsURI string) (string, error) {
	parts, err := _parseGCSURI(gcsURI)
	if err != nil {
		return "", err
	}
	// Keep the original logic to remove a trailing slash from the path
	return strings.TrimRight(parts["path"], "/"), nil
}

// base64EncodeString encodes a string to base64.
func encodeBase64(input string) string {
	return base64.StdEncoding.EncodeToString([]byte(input))
}

// dirnameFromFlag extracts the directory from the value of a flag like --path=/some/directory/file
func dirnameFromFlag(flag string) string {
	parts := strings.SplitN(flag, "=", 2)
	if len(parts) == 2 {
		return filepath.Dir(parts[1])
	}
	return "/config" // Default if the flag is not in the expected format
}

func extractStringAfterSlash(input string) (string, error) {
	// Find the last occurrence of a slash.
	lastSlashIndex := strings.LastIndex(input, "/")

	// If no slash is found, return the original string without an error.
	if lastSlashIndex == -1 {
		return input, nil
	}

	// If a slash is found, return the part of the string that comes after it.
	return input[lastSlashIndex+1:], nil
}

// resolveModelData now gets the result by reading the status of the ModelData CR.
// This is the "consumer" side of the final architecture.
func resolveModelData(
	dynClient dynamic.Interface,
	mapper meta.RESTMapper,
	namespace, modelDataName string,
) (map[string]string, error) {
	// 1. Get the ModelData CR object using the dynamic client
	modelDataGVK := schema.GroupKind{Group: "model.skippy.io", Kind: "ModelData"}
	gvr, err := mapper.RESTMapping(modelDataGVK)
	if err != nil {
		return nil, fmt.Errorf("failed to get mapping for ModelData GVK: %w", err)
	}
	modelDataCR, err := dynClient.Resource(gvr.Resource).Namespace(namespace).Get(context.Background(), modelDataName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// The ModelData CR itself doesn't exist yet.
			return nil, fmt.Errorf("waiting for ModelData resource %q to be created", modelDataName)
		}
		return nil, fmt.Errorf("failed to get referenced ModelData %q: %w", modelDataName, err)
	}

	// 2. Check the `.status.phase` field.
	phase, found, _ := unstructured.NestedString(modelDataCR.Object, "status", "phase")
	if !found || phase != "Succeeded" {
		// The status is not yet "Succeeded". Return an error to trigger a requeue.
		return nil, fmt.Errorf("waiting for ModelData %q to have phase 'Succeeded' (current phase: %q)", modelDataName, phase)
	}

	// 3. Success! The status is Succeeded. Read the final path from the status.
	finalGCSPath, found, _ := unstructured.NestedString(modelDataCR.Object, "status", "finalGcsPath")
	if !found || finalGCSPath == "" {
		return nil, fmt.Errorf("ModelData %q succeeded, but status.finalGcsPath is missing", modelDataName)
	}

	// 4. Build and return the final --model argument.
	modelDir, _ := getGcsPathFromURI(finalGCSPath) // Use our robust helper
	finalModelArg := fmt.Sprintf("--model=/data/%s", modelDir)

	return map[string]string{
		"modelArg": finalModelArg,
		"gcsPath":  finalGCSPath,
	}, nil
}

// toStringSlice converts a []interface{} to []string.
// It returns an error if any element is not a string.
func toStringSlice(v interface{}) ([]string, error) {
	if v == nil {
		return nil, nil
	}

	s, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected []interface{}, got %T", v)
	}

	res := make([]string, len(s))
	for i, u := range s {
		str, ok := u.(string)
		if !ok {
			return nil, fmt.Errorf("element at index %d is not a string, got %T", i, u)
		}
		res[i] = str
	}
	return res, nil
}

// Custom join function that handles the conversion.
func joinInterfaceSlice(v interface{}, sep string) (string, error) {
	s, err := toStringSlice(v)
	if err != nil {
		return "", fmt.Errorf("failed to convert to []string for join: %w", err)
	}
	return strings.Join(s, sep), nil
}

func findResource(resources map[string]interface{}, kind, name string) (map[string]interface{}, error) {
	// Construct the key just like it's created in your transformer.
	key := fmt.Sprintf("%s/%s", kind, name)

	// Look up the resource in the map.
	resource, ok := resources[key]
	if !ok {
		return nil, fmt.Errorf("could not find resource with key %q in the resource map", key)
	}

	// Type-assert the found resource back to the expected map structure.
	resourceMap, ok := resource.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("resource with key %q is not of the expected type map[string]interface{}", key)
	}

	return resourceMap, nil
}
