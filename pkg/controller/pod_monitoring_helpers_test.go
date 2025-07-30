package controller

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Helper function to create an unstructured PodMonitoring object for testing.
// Pass nil to fields to omit them.
func newUnstructuredPodMonitoring(t *testing.T, name string, selectorLabels map[string]string, endpoints []interface{}, targetMetadataLabels []string) *unstructured.Unstructured {
	spec := map[string]interface{}{}

	if selectorLabels != nil {
		matchLabelsMap := make(map[string]interface{}, len(selectorLabels))
		for k, v := range selectorLabels {
			matchLabelsMap[k] = v
		}
		spec["selector"] = map[string]interface{}{
			"matchLabels": matchLabelsMap, // Now it's the correct type
		}
	}

	if endpoints != nil {
		spec["endpoints"] = endpoints
	}

	if targetMetadataLabels != nil {
		// You MUST convert the []string into a []interface{} for the unstructured map.
		metadataLabelsAsInterface := make([]interface{}, len(targetMetadataLabels))
		for i, v := range targetMetadataLabels {
			metadataLabelsAsInterface[i] = v
		}
		spec["targetLabels"] = map[string]interface{}{
			"metadata": metadataLabelsAsInterface, // Assign the correctly typed slice
		}
	}

	pm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "monitoring.coreos.com/v1", // Use your actual GVK
			"kind":       "PodMonitor",               // Use your actual Kind
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
			"spec": spec,
		},
	}
	return pm
}

// It's good practice to test the core extraction logic directly.
func TestExtractSelector(t *testing.T) {
	logger := testLogger()
	testCases := []struct {
		name             string
		inputSpec        map[string]interface{}
		expectedSelector map[string]string
		expectError      bool
	}{
		{
			name: "valid selector with matchLabels",
			inputSpec: map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": "my-app", "env": "prod"},
				},
			},
			expectedSelector: map[string]string{"app": "my-app", "env": "prod"},
			expectError:      false,
		},
		{
			name:             "no selector field",
			inputSpec:        map[string]interface{}{},
			expectedSelector: nil,
			expectError:      false,
		},
		{
			name: "selector field exists but has no matchLabels",
			inputSpec: map[string]interface{}{
				"selector": map[string]interface{}{},
			},
			expectedSelector: nil,
			expectError:      false,
		},
		{
			name: "matchLabels is not a map",
			inputSpec: map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": "not-a-map",
				},
			},
			expectedSelector: nil,
			expectError:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			selector, err := extractSelector(tc.inputSpec, logger)
			if (err != nil) != tc.expectError {
				t.Fatalf("extractSelector() error = %v, wantErr %v", err, tc.expectError)
			}
			if !reflect.DeepEqual(selector, tc.expectedSelector) {
				t.Errorf("extractSelector() = %v, want %v", selector, tc.expectedSelector)
			}
		})
	}
}

// Also test extractEndpoints and extractTargetLabels similarly if they become more complex.

func TestPodMonitoringDiff(t *testing.T) {
	logger := testLogger()
	r := &GenericReconciler{} // For calling the podMonitoringDiff method

	// Define some common components for tests
	baseSelector := map[string]string{"app": "nginx"}
	baseEndpoints := []interface{}{
		map[string]interface{}{"port": "web", "interval": "30s"},
	}
	baseTargetLabels := []string{"label1", "label2"}

	testCases := []struct {
		name        string
		existingPM  *unstructured.Unstructured
		desiredPM   *unstructured.Unstructured
		expectDiff  bool
		expectError bool
	}{
		{
			name:       "identical PodMonitors",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			expectDiff: false,
		},
		{
			name:       "different selector label value",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", map[string]string{"app": "apache"}, baseEndpoints, baseTargetLabels),
			expectDiff: true,
		},
		{
			name:       "one has selector, other does not",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", nil, baseEndpoints, baseTargetLabels),
			expectDiff: true,
		},
		{
			name:       "different endpoint port",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", baseSelector, []interface{}{map[string]interface{}{"port": "metrics", "interval": "30s"}}, baseTargetLabels),
			expectDiff: true,
		},
		{
			name:       "different number of endpoints",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", baseSelector, append(baseEndpoints, map[string]interface{}{"port": "metrics"}), baseTargetLabels),
			expectDiff: true,
		},
		{
			name: "same endpoints but different order (should be different if not sorted)",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, []interface{}{
				map[string]interface{}{"port": "web"},
				map[string]interface{}{"port": "metrics"},
			}, baseTargetLabels),
			desiredPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, []interface{}{
				map[string]interface{}{"port": "metrics"},
				map[string]interface{}{"port": "web"},
			}, baseTargetLabels),
			// Note: Your current extractEndpoints does not sort. So DeepEqual will see this as a diff.
			// This might be the desired behavior. If not, add sorting to extractEndpoints.
			expectDiff: true,
		},
		{
			name:       "different target labels",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, []string{"label1", "label3"}),
			expectDiff: true,
		},
		{
			name:       "same target labels but different order (should NOT be a diff)",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, []string{"label2", "label1"}),
			desiredPM:  newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, []string{"label1", "label2"}),
			// Note: This test verifies that your sorting logic in extractTargetLabels works correctly.
			expectDiff: false,
		},
		{
			name:       "malformed desired object",
			existingPM: newUnstructuredPodMonitoring(t, "test-pm", baseSelector, baseEndpoints, baseTargetLabels),
			desiredPM: &unstructured.Unstructured{
				Object: map[string]interface{}{"spec": "not-a-map"},
			},
			expectError: true, // getPodMonitoringDetails should return an error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := r.podMonitoringDiff(tc.existingPM, tc.desiredPM, logger)

			if (err != nil) != tc.expectError {
				t.Fatalf("podMonitoringDiff() error = %v, wantErr %v", err, tc.expectError)
			}
			if err != nil {
				return // If an error was expected and occurred, no need to check diff
			}

			if diff != tc.expectDiff {
				t.Errorf("podMonitoringDiff() = %v, want %v", diff, tc.expectDiff)
			}
		})
	}
}
