package controller

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// Helper to create a logger for tests if not already available in a shared file
// func testLogger() logr.Logger {
// 	return log.Log.WithName("test-configmap-helper")
// }

// Helper function to create an unstructured ConfigMap for testing.
// It accepts map[string]interface{} for data to allow testing of malformed values.
func newUnstructuredConfigMap(t *testing.T, name string, data map[string]interface{}) *unstructured.Unstructured {
	cm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
		},
	}
	if data != nil {
		cm.Object["data"] = data
	}
	return cm
}

func TestGetConfigMapData(t *testing.T) {
	logger := testLogger()

	testCases := []struct {
		name         string
		inputObj     runtime.Object
		expectedData map[string]string
	}{
		{
			name: "valid configmap with string data",
			inputObj: newUnstructuredConfigMap(t, "test-cm-1", map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			}),
			expectedData: map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name: "configmap with mixed data types",
			inputObj: newUnstructuredConfigMap(t, "test-cm-2", map[string]interface{}{
				"key1": "value1",
				"key2": 123,  // Should be skipped
				"key3": true, // Should be skipped
				"key4": "value4",
			}),
			expectedData: map[string]string{"key1": "value1", "key4": "value4"},
		},
		{
			name:         "configmap with empty data map",
			inputObj:     newUnstructuredConfigMap(t, "test-cm-3", map[string]interface{}{}),
			expectedData: map[string]string{},
		},
		{
			name:         "configmap with no data field",
			inputObj:     newUnstructuredConfigMap(t, "test-cm-4", nil),
			expectedData: nil,
		},
		{
			name: "data field is not a map",
			inputObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "ConfigMap",
					"data": "this-is-not-a-map",
				},
			},
			expectedData: nil,
		},
		{
			name:         "input object is not unstructured.Unstructured",
			inputObj:     &corev1.ConfigMap{}, // Pass a typed object instead
			expectedData: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotData := getConfigMapData(tc.inputObj, logger)
			if !reflect.DeepEqual(gotData, tc.expectedData) {
				t.Errorf("getConfigMapData() got = %v, want %v", gotData, tc.expectedData)
			}
		})
	}
}

func TestConfigMapDiff(t *testing.T) {
	logger := testLogger()
	r := &GenericReconciler{}

	testCases := []struct {
		name       string
		existingCM *unstructured.Unstructured
		desiredCM  *unstructured.Unstructured
		expectDiff bool
	}{
		{
			name:       "identical ConfigMaps",
			existingCM: newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value1"}),
			desiredCM:  newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value1"}),
			expectDiff: false,
		},
		{
			name:       "different data value",
			existingCM: newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value1"}),
			desiredCM:  newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value2"}),
			expectDiff: true,
		},
		{
			name:       "desired has extra data key",
			existingCM: newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value1"}),
			desiredCM:  newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value1", "key2": "value2"}),
			expectDiff: true,
		},
		{
			name:       "one has data, other does not",
			existingCM: newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{"key1": "value1"}),
			desiredCM:  newUnstructuredConfigMap(t, "test-cm", nil),
			expectDiff: true,
		},
		{
			name:       "both have no data",
			existingCM: newUnstructuredConfigMap(t, "test-cm", nil),
			desiredCM:  newUnstructuredConfigMap(t, "test-cm", nil),
			expectDiff: false,
		},
		{
			name: "semantically identical after filtering non-string data",
			existingCM: newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{
				"key1": "value1",
			}),
			desiredCM: newUnstructuredConfigMap(t, "test-cm", map[string]interface{}{
				"key1":        "value1",
				"ignored_key": 12345, // This key should be filtered out by getConfigMapData
			}),
			expectDiff: false, // The resulting maps should be identical after filtering
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := r.configMapDiff(tc.existingCM, tc.desiredCM, logger)
			if err != nil {
				t.Fatalf("configMapDiff returned an unexpected error: %v", err)
			}
			if diff != tc.expectDiff {
				t.Errorf("configMapDiff() = %v, want %v", diff, tc.expectDiff)
			}
		})
	}
}
