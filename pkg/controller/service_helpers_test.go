// In service_helpers_test.go

package controller

import (
	"testing"

	"github.com/google/go-cmp/cmp" // Use go-cmp for better test failure messages
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// newTestService creates an unstructured.Unstructured service for testing.
// It allows adding extra fields to simulate system-assigned values.
func newTestService(spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      "test-service",
				"namespace": "default",
			},
			"spec": spec,
		},
	}
}

func TestServiceDiff(t *testing.T) {
	reconciler := &GenericReconciler{}
	logger := testLogger()

	tests := []struct {
		name        string
		existingObj *unstructured.Unstructured
		desiredObj  *unstructured.Unstructured
		expectDiff  bool
	}{
		// --- THIS IS THE MOST IMPORTANT TEST CASE ---
		{
			name: "existing service has system-assigned fields, should not diff",
			existingObj: newTestService(map[string]interface{}{
				"type":     "LoadBalancer",
				"selector": map[string]interface{}{"app": "my-app"},
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(80), "targetPort": int64(8080), "nodePort": int64(31234)}, // Has nodePort
				},
				"clusterIP":             "10.0.0.123", // System-assigned field
				"internalTrafficPolicy": "Cluster",    // System-assigned field
			}),
			desiredObj: newTestService(map[string]interface{}{
				"type":     "LoadBalancer",
				"selector": map[string]interface{}{"app": "my-app"},
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(80), "targetPort": int64(8080)}, // No nodePort
				},
			}),
			expectDiff: false, // Expect NO diff because cleanServiceSpec ignores system fields
		},
		{
			name: "identical simple services",
			existingObj: newTestService(map[string]interface{}{
				"type":     "ClusterIP",
				"selector": map[string]interface{}{"app": "my-app"},
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(80)},
				},
			}),
			desiredObj: newTestService(map[string]interface{}{
				"type":     "ClusterIP",
				"selector": map[string]interface{}{"app": "my-app"},
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(80)},
				},
			}),
			expectDiff: false,
		},
		{
			name: "ports are in a different order, should not diff",
			existingObj: newTestService(map[string]interface{}{
				"type": "ClusterIP",
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(80)},
					map[string]interface{}{"name": "https", "port": int64(443)},
				},
			}),
			desiredObj: newTestService(map[string]interface{}{
				"type": "ClusterIP",
				"ports": []interface{}{
					map[string]interface{}{"name": "https", "port": int64(443)},
					map[string]interface{}{"name": "http", "port": int64(80)},
				},
			}),
			expectDiff: false, // Sorting should handle this
		},
		{
			name: "different service type, should diff",
			existingObj: newTestService(map[string]interface{}{
				"type": "ClusterIP",
			}),
			desiredObj: newTestService(map[string]interface{}{
				"type": "LoadBalancer",
			}),
			expectDiff: true,
		},
		{
			name: "different selector, should diff",
			existingObj: newTestService(map[string]interface{}{
				"selector": map[string]interface{}{"app": "my-app-v1"},
			}),
			desiredObj: newTestService(map[string]interface{}{
				"selector": map[string]interface{}{"app": "my-app-v2"},
			}),
			expectDiff: true,
		},
		{
			name: "different port number, should diff",
			existingObj: newTestService(map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(80)},
				},
			}),
			desiredObj: newTestService(map[string]interface{}{
				"ports": []interface{}{
					map[string]interface{}{"name": "http", "port": int64(81)},
				},
			}),
			expectDiff: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := reconciler.serviceDiff(tc.existingObj, tc.desiredObj, logger)

			if err != nil {
				t.Fatalf("serviceDiff() returned unexpected error: %v", err)
			}
			if diff != tc.expectDiff {
				// Use cmp.Diff for a readable error message
				cleanedExisting := cleanServiceSpec(tc.existingObj.Object["spec"].(map[string]interface{}), logger)
				cleanedDesired := cleanServiceSpec(tc.desiredObj.Object["spec"].(map[string]interface{}), logger)
				t.Errorf("serviceDiff() got diff = %v, want %v\n--- diff ---\n%s",
					diff, tc.expectDiff, cmp.Diff(cleanedExisting, cleanedDesired))
			}
		})
	}
}
