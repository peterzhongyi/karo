package transformer

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// --- Helper Functions ---

// --- Main Test Function ---

func TestTopologicalSort(t *testing.T) {
	// Define GVKs for our test objects from your example
	endpointGVK := schema.GroupVersionKind{Group: "model.skippy.io", Version: "v1", Kind: "Endpoint"}
	monitorGVK := schema.GroupVersionKind{Group: "model.skippy.io", Version: "v1", Kind: "Monitor"}

	// This defines the reference rule from your YAML: Monitor -> Endpoint
	monitorRefPaths := map[schema.GroupVersionKind][]modelv1.IntegrationApiReferenceSpec{ // <-- Correct type
		monitorGVK: { // <-- The value is now a slice literal
			{ // <-- Containing one IntegrationApiReferenceSpec struct
				Group:   "model.skippy.io",
				Version: "v1",
				Kind:    "Endpoint",
				Paths:   modelv1.IntegrationApiReferencePathSpec{Name: "spec.endpoint.name"},
			},
		},
	}

	testCases := []struct {
		name          string
		inputObjects  []*unstructured.Unstructured
		mockRefPaths  map[schema.GroupVersionKind][]modelv1.IntegrationApiReferenceSpec
		expectedOrder []string // List of object keys in expected sorted order (Kind/Name)
		expectError   bool
	}{
		{
			name: "Monitor depends on Endpoint",
			inputObjects: []*unstructured.Unstructured{
				addRef(newTestObject("model.skippy.io", "v1", "Monitor", "my-monitor"), "my-endpoint", "spec.endpoint.name"), // Monitor depends on Endpoint
				newTestObject("model.skippy.io", "v1", "Endpoint", "my-endpoint"),                                            // Endpoint has no dependencies
			},
			mockRefPaths: monitorRefPaths,
			// The sort is REVERSED at the end, so dependents come first.
			expectedOrder: []string{"Endpoint/my-endpoint", "Monitor/my-monitor"},
			expectError:   false,
		},
		{
			name: "no dependencies",
			inputObjects: []*unstructured.Unstructured{
				newTestObject("model.skippy.io", "v1", "Monitor", "my-monitor"),
				newTestObject("model.skippy.io", "v1", "Endpoint", "my-endpoint"),
				newTestObject("model.skippy.io", "v1", "SomeOtherResource", "other"),
			},
			mockRefPaths:  nil, // <-- THE FIX: No dependency rules for this test.
			expectedOrder: []string{"Monitor/my-monitor", "Endpoint/my-endpoint", "SomeOtherResource/other"},
			expectError:   false,
		},
		{
			name: "cycle detected: Monitor -> Endpoint -> Monitor",
			inputObjects: []*unstructured.Unstructured{
				addRef(newTestObject("model.skippy.io", "v1", "Monitor", "mon-1"), "ep-1", "spec.endpoint.name"),
				addRef(newTestObject("model.skippy.io", "v1", "Endpoint", "ep-1"), "mon-1", "spec.monitor.name"),
			},
			mockRefPaths: map[schema.GroupVersionKind][]modelv1.IntegrationApiReferenceSpec{ // <-- Correct type
				monitorGVK:  {{Group: "model.skippy.io", Version: "v1", Kind: "Endpoint", Paths: modelv1.IntegrationApiReferencePathSpec{Name: "spec.endpoint.name"}}},
				endpointGVK: {{Group: "model.skippy.io", Version: "v1", Kind: "Monitor", Paths: modelv1.IntegrationApiReferencePathSpec{Name: "spec.monitor.name"}}},
			},
			expectError: true,
		},
		{
			name: "reference path defined but missing from object",
			inputObjects: []*unstructured.Unstructured{
				newTestObject("model.skippy.io", "v1", "Endpoint", "my-endpoint"),
				newTestObject("model.skippy.io", "v1", "Monitor", "my-monitor"), // Monitor spec is empty, missing spec.endpoint.name
			},
			mockRefPaths: monitorRefPaths,
			expectError:  true, // getReferencedObjectKeys should return an error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// ARRANGE
			transformer := &Transformer{
				registry: &mockRegistry{refPaths: tc.mockRefPaths},
			}

			// ACT
			sortedList, err := transformer.topologicalSort(tc.inputObjects)

			// ASSERT
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error but got none")
				}
				return // Test is successful if an error was expected and received.
			}

			if err != nil {
				t.Fatalf("Received an unexpected error: %v", err)
			}

			if len(sortedList) != len(tc.inputObjects) {
				t.Fatalf("Expected sorted list to have length %d, but got %d", len(tc.inputObjects), len(sortedList))
			}

			// Convert sorted list to a list of keys for easier comparison
			sortedKeys := make([]string, len(sortedList))
			for i, obj := range sortedList {
				sortedKeys[i] = fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())
			}

			// For tests where a specific order is expected
			if tc.name == "Monitor depends on Endpoint" {
				if !reflect.DeepEqual(sortedKeys, tc.expectedOrder) {
					t.Errorf("topologicalSort() order got = %v, want %v", sortedKeys, tc.expectedOrder)
				}
			} else { // For tests where order is not guaranteed, check content
				sort.Strings(sortedKeys)
				sort.Strings(tc.expectedOrder)
				if !reflect.DeepEqual(sortedKeys, tc.expectedOrder) {
					t.Errorf("topologicalSort() content got = %v, want %v", sortedKeys, tc.expectedOrder)
				}
			}
		})
	}
}

// Helper for chaining addReference calls, makes test setup cleaner.
func addRef(obj *unstructured.Unstructured, refName string, path string) *unstructured.Unstructured {
	addReference(obj, refName, path)
	return obj
}
