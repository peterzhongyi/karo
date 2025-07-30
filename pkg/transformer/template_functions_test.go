// filename: template_functions_test.go
package transformer

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	template "github.com/google/safetext/yamltemplate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestExtractValueAfterEquals(t *testing.T) {
	testCases := []struct {
		name        string
		args        []interface{}
		key         string
		expectedVal string
		// The expectErr field is no longer needed as the function never errors.
	}{
		{
			name:        "valid case with key",
			args:        []interface{}{"--other=foo", "--port=8080", "--another=baz"},
			key:         "--port",
			expectedVal: "8080",
		},
		{
			name:        "value with extra space",
			args:        []interface{}{"--port= 8080 "},
			key:         "--port",
			expectedVal: "8080",
		},
		{
			name:        "key not found returns empty string",
			args:        []interface{}{"--other=foo"},
			key:         "--port",
			expectedVal: "", // Expect empty string, not error
		},
		{
			name:        "value after equals is empty returns empty string",
			args:        []interface{}{"--port="},
			key:         "--port",
			expectedVal: "", // Expect empty string, not error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// The function no longer returns an error.
			val, err := extractValueAfterEquals(tc.args, tc.key)

			require.NoError(t, err)
			assert.Equal(t, tc.expectedVal, val)
		})
	}
}

func TestMinPerformanceAccelerator(t *testing.T) {
	bestOption := map[string]interface{}{
		"acceleratorType": "nvidia-tesla-v100",
		"performanceStats": map[string]interface{}{
			"outputTokensPerSecond": 100.5,
		},
	}
	goodOption := map[string]interface{}{
		"acceleratorType": "nvidia-tesla-a100",
		"performanceStats": map[string]interface{}{
			"outputTokensPerSecond": 200, // Worse performance
		},
	}
	tpuOption := map[string]interface{}{
		"acceleratorType": "tpu-v4",
		"performanceStats": map[string]interface{}{
			"outputTokensPerSecond": 50.0,
		},
	}
	noStatsOption := map[string]interface{}{
		"acceleratorType": "nvidia-tesla-k80",
	}

	testCases := []struct {
		name     string
		options  []interface{}
		expected map[string]interface{}
	}{
		{
			name:     "finds best option",
			options:  []interface{}{goodOption, bestOption},
			expected: bestOption,
		},
		{
			name:     "ignores non-nvidia accelerators",
			options:  []interface{}{goodOption, bestOption, tpuOption},
			expected: bestOption,
		},
		{
			name:     "handles missing stats",
			options:  []interface{}{goodOption, noStatsOption, bestOption},
			expected: bestOption,
		},
		{
			name:     "no valid options returns nil",
			options:  []interface{}{tpuOption, noStatsOption},
			expected: nil,
		},
		{
			name:     "empty input returns nil",
			options:  []interface{}{},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := minPerformanceAccelerator(tc.options)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestGetGcsBucketFromURI(t *testing.T) {
	t.Run("valid URI", func(t *testing.T) {
		bucket, err := getGcsBucketFromURI("gs://model-data-meta-llama/meta-llama/Llama-3.1-8B-Instruct/0e9e39f249a16976918f6564b8830bc894c89659")
		require.NoError(t, err)
		assert.Equal(t, "model-data-meta-llama", bucket)
	})

	t.Run("non-gcs-uri string returns empty without error", func(t *testing.T) {
		bucket, err := getGcsBucketFromURI("baseline")
		require.NoError(t, err)
		assert.Equal(t, "", bucket)
	})

	t.Run("empty string returns empty without error", func(t *testing.T) {
		bucket, err := getGcsBucketFromURI("  ")
		require.NoError(t, err)
		assert.Equal(t, "", bucket)
	})

	t.Run("malformed gs URI still errors", func(t *testing.T) {
		_, err := getGcsBucketFromURI("gs://")
		assert.Error(t, err)
	})
}

func TestGetGcsPathFromURI(t *testing.T) {
	t.Run("valid path", func(t *testing.T) {
		path, err := getGcsPathFromURI("gs://my-bucket/path/to/object.txt")
		require.NoError(t, err)
		assert.Equal(t, "path/to/object.txt", path)
	})

	t.Run("path with trailing slash", func(t *testing.T) {
		path, err := getGcsPathFromURI("gs://my-bucket/path/to/folder/")
		require.NoError(t, err)
		assert.Equal(t, "path/to/folder", path)
	})

	t.Run("non-gcs-uri string returns empty without error", func(t *testing.T) {
		path, err := getGcsPathFromURI("baseline")
		require.NoError(t, err)
		assert.Equal(t, "", path)
	})
}

func TestEncodeBase64(t *testing.T) {
	assert.Equal(t, "aGVsbG8gd29ybGQ=", encodeBase64("hello world"))
	assert.Equal(t, "", encodeBase64(""))
}

func TestDirnameFromFlag(t *testing.T) {
	assert.Equal(t, "/some/dir", dirnameFromFlag("--path=/some/dir/file.txt"))
	assert.Equal(t, "/", dirnameFromFlag("--path=/file.txt"))
	assert.Equal(t, "/config", dirnameFromFlag("--path")) // No '='
	assert.Equal(t, ".", dirnameFromFlag("--path="))      // Empty path
}

func TestExtractStringAfterSlash(t *testing.T) {
	t.Run("valid case with one slash", func(t *testing.T) {
		val, err := extractStringAfterSlash("type/name")
		require.NoError(t, err)
		assert.Equal(t, "name", val)
	})

	t.Run("no slash returns original string", func(t *testing.T) {
		val, err := extractStringAfterSlash("baseline")
		require.NoError(t, err)
		assert.Equal(t, "baseline", val)
	})

	t.Run("multiple slashes returns part after last slash", func(t *testing.T) {
		val, err := extractStringAfterSlash("a/b/c/d")
		require.NoError(t, err)
		assert.Equal(t, "d", val)
	})

	t.Run("empty string returns empty string", func(t *testing.T) {
		val, err := extractStringAfterSlash("")
		require.NoError(t, err)
		assert.Equal(t, "", val)
	})
}

func TestResolveModelData(t *testing.T) {
	const namespace = "default"
	const modelDataName = "test-model-data"
	const finalGcsPath = "gs://my-bucket/my-org/my-model/abcdef12345"
	const expectedModelArg = "--model=/data/my-org/my-model/abcdef12345"

	// Helper to create a ModelData CR with a specific phase and path in its status
	makeFakeModelDataWithStatus := func(name, phase, path string) *unstructured.Unstructured {
		cr := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "model.skippy.io/v1",
				"kind":       "ModelData",
				"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
				"spec":       map[string]interface{}{},
				"status": map[string]interface{}{
					"phase":        phase,
					"finalGcsPath": path,
				},
			},
		}
		return cr
	}

	testCases := []struct {
		name                string
		initialUnstructured []*unstructured.Unstructured
		// FIX: The expected result is now a map, not a single string
		expectedResult    map[string]string
		expectErrContains string
	}{
		{
			name:                "Happy Path - Status is Succeeded",
			initialUnstructured: []*unstructured.Unstructured{makeFakeModelDataWithStatus(modelDataName, "Succeeded", finalGcsPath)},
			// FIX: The expected result is now a map containing both values
			expectedResult:    map[string]string{"modelArg": expectedModelArg, "gcsPath": finalGcsPath},
			expectErrContains: "",
		},
		{
			name:                "Waiting state - Phase is Syncing",
			initialUnstructured: []*unstructured.Unstructured{makeFakeModelDataWithStatus(modelDataName, "Syncing", "")},
			expectedResult:      nil, // On error, the result is nil
			expectErrContains:   "to have phase 'Succeeded' (current phase: \"Syncing\")",
		},
		{
			name:                "Error state - Succeeded but path is missing",
			initialUnstructured: []*unstructured.Unstructured{makeFakeModelDataWithStatus(modelDataName, "Succeeded", "")},
			expectedResult:      nil,
			expectErrContains:   "status.finalGcsPath is missing",
		},
	}

	mockMapper := &mockRESTMapper{}
	// The new function signature requires a typed client, so we create a fake one.
	// Even though this simplified function doesn't use it, we must provide it.

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// ARRANGE
			var runtimeObjects []runtime.Object
			for _, obj := range tc.initialUnstructured {
				runtimeObjects = append(runtimeObjects, obj)
			}
			fakeDynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), runtimeObjects...)

			// ACT - The call now passes the fakeTypedClient, but it's not used in this version.
			// This call signature matches the more complex version that checks Jobs/Pods.
			result, err := resolveModelData(fakeDynClient, mockMapper, namespace, modelDataName)

			// ASSERT
			if tc.expectErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectErrContains)
			} else {
				require.NoError(t, err)
				// FIX: The assertion now compares the expected map to the result map.
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

// Add this new test function to your file

func TestAcceleratorSelectionTemplateLogic(t *testing.T) {
	// Step 1: The exact JSON data you provided from the recommender API call.
	// We unmarshal this into a generic map, just like your controller does.
	jsonData := `
	{
	  "acceleratorOptions": [
	    {
	      "acceleratorType": "nvidia-l4",
	      "resourcesUsed": { "acceleratorCount": 1 },
	      "performanceStats": { "outputTokensPerSecond": 4784 }
	    },
	    {
	      "acceleratorType": "nvidia-tesla-a100",
	      "resourcesUsed": { "acceleratorCount": 1 },
	      "performanceStats": { "outputTokensPerSecond": 14938 }
	    }
	  ]
	}
	`
	var aireData interface{}
	err := json.Unmarshal([]byte(jsonData), &aireData)
	require.NoError(t, err, "Failed to unmarshal test JSON data")

	// This is the full context map we will pass to the template
	templateContext := map[string]interface{}{
		"aire": aireData,
	}

	// Step 2: The template logic we want to test.
	// This calls your Go function and then tries to access the nested fields.
	// It outputs the results in a simple key=value format for easy checking.
	templateLogic := `
		{{- $bestOption := minPerformanceAccelerator .aire.acceleratorOptions -}}
		{{- if $bestOption -}}
			type={{ index $bestOption "acceleratorType" }},count={{ index (index $bestOption "resourcesUsed") "acceleratorCount" }}
		{{- else -}}
			no-option-found
		{{- end -}}
	`

	// Step 3: Execute the template with the data.
	// We need to use a new template instance for this isolated test.
	tmpl, err := template.New("accel-test").Funcs(allTemplateFuncs).Parse(templateLogic)
	require.NoError(t, err, "Failed to parse template logic")

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, templateContext)
	require.NoError(t, err, "Failed to execute template")

	// Step 4: Assert the result.
	result := strings.TrimSpace(buf.String())
	expected := "type=nvidia-l4,count=1"

	assert.Equal(t, expected, result, "The template did not extract the nested values correctly")

	t.Logf("Template successfully produced: %s", result)
}
