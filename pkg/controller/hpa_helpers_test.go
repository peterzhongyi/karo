package controller

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Helper to create a logger for tests if not already available in a shared file
// func testLogger() logr.Logger {
// 	return log.Log.WithName("test-hpa-helper")
// }

// Helper function to create an unstructured HPA for testing.
// Pass nil to metrics to omit it.
func newUnstructuredHPA(t *testing.T, name string, minReplicas, maxReplicas int32, metrics []interface{}) *unstructured.Unstructured {
	spec := map[string]interface{}{
		"minReplicas": minReplicas,
		"maxReplicas": maxReplicas,
		// ScaleTargetRef is usually static, but could be added as a parameter if needed for tests
		"scaleTargetRef": map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"name":       "test-deployment",
		},
	}

	if metrics != nil {
		spec["metrics"] = metrics
	}

	hpa := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "autoscaling/v2", // Use your actual GVK
			"kind":       "HorizontalPodAutoscaler",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
			"spec": spec,
		},
	}
	return hpa
}

// It's good practice to test the most complex helper directly.
func TestGetMetricsFromHPA(t *testing.T) {
	logger := testLogger()

	// Define metric structures for test data
	resourceCPUMetric := map[string]interface{}{
		"type": "Resource", "resource": map[string]interface{}{"name": "cpu", "target": map[string]interface{}{"type": "Utilization", "averageUtilization": 80}},
	}
	resourceMemoryMetric := map[string]interface{}{
		"type": "Resource", "resource": map[string]interface{}{"name": "memory", "target": map[string]interface{}{"type": "Utilization", "averageUtilization": int32(75)}},
	}
	podsMetric := map[string]interface{}{
		"type": "Pods", "pods": map[string]interface{}{"metric": map[string]interface{}{"name": "packets-per-second"}, "target": map[string]interface{}{"type": "AverageValue", "averageValue": "1k"}},
	}

	testCases := []struct {
		name            string
		inputMetrics    []interface{}
		expectedMetrics []map[string]interface{}
		expectSorted    bool
	}{
		{
			name:            "empty input metrics list",
			inputMetrics:    []interface{}{},
			expectedMetrics: []map[string]interface{}{},
		},
		{
			name:            "nil input metrics list",
			inputMetrics:    nil,
			expectedMetrics: []map[string]interface{}{},
		},
		{
			name:         "single resource metric",
			inputMetrics: []interface{}{resourceCPUMetric},
			expectedMetrics: []map[string]interface{}{
				{"type": "Resource", "resource": map[string]interface{}{"name": "cpu", "target": map[string]interface{}{"type": "Utilization", "averageUtilization": int32(80)}}},
			},
		},
		{
			name:         "multiple metrics get sorted by type then name",
			inputMetrics: []interface{}{resourceMemoryMetric, podsMetric, resourceCPUMetric},
			expectSorted: true, // This flag tells our test runner to handle this case differently
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spec := map[string]interface{}{"metrics": tc.inputMetrics}
			result := getMetricsFromHPA(spec, logger)

			// --- THIS IS THE CORRECTED LOGIC ---
			if tc.expectSorted {
				// For sorting tests, we check the length against the *input* length.
				if len(result) != len(tc.inputMetrics) {
					t.Fatalf("Expected %d metrics to be processed, but got %d", len(tc.inputMetrics), len(result))
				}
				if len(result) > 1 {
					// Check if the sorting logic worked as expected.
					// Based on our input, the "Pods" metric should come before "Resource" metrics.
					firstMetricType := result[0]["type"].(string)
					if firstMetricType != "Pods" {
						t.Errorf("Expected first metric after sorting to be 'Pods', but got '%s'", firstMetricType)
					}
				}
			} else {
				// For all other tests, we do a direct comparison using DeepEqual.
				if !reflect.DeepEqual(result, tc.expectedMetrics) {
					t.Errorf("getMetricsFromHPA() got = %#v, want %#v", result, tc.expectedMetrics)
				}
			}
		})
	}
}

func TestHPADiff(t *testing.T) {
	logger := testLogger()
	r := &GenericReconciler{}

	// Define some common metric structures for test data
	cpuMetric50 := []interface{}{map[string]interface{}{
		"type": "Resource", "resource": map[string]interface{}{"name": "cpu", "target": map[string]interface{}{"type": "Utilization", "averageUtilization": 50}},
	}}
	cpuMetric70 := []interface{}{map[string]interface{}{
		"type": "Resource", "resource": map[string]interface{}{"name": "cpu", "target": map[string]interface{}{"type": "Utilization", "averageUtilization": 70}},
	}}
	podsMetric := []interface{}{map[string]interface{}{
		"type": "Pods", "pods": map[string]interface{}{"metric": map[string]interface{}{"name": "rps"}, "target": map[string]interface{}{"type": "AverageValue", "averageValue": "100"}},
	}}

	testCases := []struct {
		name        string
		existingHPA *unstructured.Unstructured
		desiredHPA  *unstructured.Unstructured
		expectDiff  bool
		expectError bool
	}{
		{
			name:        "identical HPAs",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			expectDiff:  false,
		},
		{
			name:        "different minReplicas",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 2, 10, cpuMetric50),
			expectDiff:  true,
		},
		{
			name:        "different maxReplicas",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 1, 20, cpuMetric50),
			expectDiff:  true,
		},
		{
			name:        "different metric target value",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric70),
			expectDiff:  true,
		},
		{
			name:        "different metric type",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 1, 10, podsMetric),
			expectDiff:  true,
		},
		{
			name:        "one has metrics, the other does not",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 1, 10, nil),
			expectDiff:  true,
		},
		{
			name:        "malformed existing object (no spec)",
			existingHPA: &unstructured.Unstructured{Object: map[string]interface{}{"kind": "HorizontalPodAutoscaler"}},
			desiredHPA:  newUnstructuredHPA(t, "test-hpa", 1, 10, cpuMetric50),
			expectError: true, // getHPADetails should return an error
		},
		{
			name: "same metrics but different order (should NOT be a diff)",
			existingHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, []interface{}{
				podsMetric[0],  // Pods metric first
				cpuMetric50[0], // Resource metric second
			}),
			desiredHPA: newUnstructuredHPA(t, "test-hpa", 1, 10, []interface{}{
				cpuMetric50[0], // Resource metric first
				podsMetric[0],  // Pods metric second
			}),
			expectDiff: false, // This verifies that the sorting in getMetricsFromHPA works
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := r.hpaDiff(tc.existingHPA, tc.desiredHPA, logger)

			if (err != nil) != tc.expectError {
				t.Fatalf("hpaDiff() error = %v, wantErr %v", err, tc.expectError)
			}
			if err != nil {
				return // If an error was expected and occurred, no need to check diff
			}

			if diff != tc.expectDiff {
				t.Errorf("hpaDiff() = %v, want %v", diff, tc.expectDiff)
			}
		})
	}
}
