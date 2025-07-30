package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Helper to create an unstructured Job for testing.
// Pass nil for containers or initContainers to omit them.
func newUnstructuredJob(t *testing.T, name, serviceAccountName string, containersData, initContainersData []map[string]interface{}) *unstructured.Unstructured {
	podSpec := map[string]interface{}{}
	if serviceAccountName != "" {
		podSpec["serviceAccountName"] = serviceAccountName
	}
	if containersData != nil {
		podSpec["containers"] = containersData
	}
	if initContainersData != nil {
		podSpec["initContainers"] = initContainersData
	}

	job := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "batch/v1",
			"kind":       "Job",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": podSpec,
				},
			},
		},
	}
	return job
}

func TestGetJobPodSpecDetails(t *testing.T) {
	logger := testLogger() // Assuming testLogger is available

	// Define some containers for testing
	containerA := detailedContainerMap("container-a", "image-a", []string{"a"}, nil, nil, nil, nil, "")
	containerB := detailedContainerMap("container-b", "image-b", []string{"b"}, nil, nil, nil, nil, "")
	initContainer := detailedContainerMap("init-a", "init-image", nil, nil, nil, nil, nil, "")

	testCases := []struct {
		name                       string
		inputJob                   *unstructured.Unstructured
		expectedSA                 string
		expectedContainerCount     int
		expectedInitContainerCount int
		expectContainersSorted     bool
		expectedFirstContainerName string
		expectedErr                bool
	}{
		{
			name:                       "valid job with all fields",
			inputJob:                   newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{containerA}, []map[string]interface{}{initContainer}),
			expectedSA:                 "sa-1",
			expectedContainerCount:     1,
			expectedInitContainerCount: 1,
			expectedFirstContainerName: "container-a",
		},
		{
			name:                       "job with multiple containers gets sorted",
			inputJob:                   newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{containerB, containerA}, nil),
			expectedSA:                 "sa-1",
			expectedContainerCount:     2,
			expectedInitContainerCount: 0,
			expectContainersSorted:     true,
			expectedFirstContainerName: "container-a", // "a" comes before "b"
		},
		{
			name:                       "job with no init containers",
			inputJob:                   newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{containerA}, nil),
			expectedSA:                 "sa-1",
			expectedContainerCount:     1,
			expectedInitContainerCount: 0,
		},
		{
			name:                       "job with no main containers",
			inputJob:                   newUnstructuredJob(t, "test-job", "sa-1", nil, []map[string]interface{}{initContainer}),
			expectedSA:                 "sa-1",
			expectedContainerCount:     0,
			expectedInitContainerCount: 1,
		},
		{
			name: "job with no pod spec",
			inputJob: &unstructured.Unstructured{
				Object: map[string]interface{}{"kind": "Job", "spec": map[string]interface{}{"template": map[string]interface{}{}}},
			},
			expectedSA:                 "",
			expectedContainerCount:     0,
			expectedInitContainerCount: 0,
			expectedErr:                false, // Function should handle this gracefully
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sa, initContainers, containers, err := getJobPodSpecDetails(tc.inputJob, logger)

			if (err != nil) != tc.expectedErr {
				t.Fatalf("getJobPodSpecDetails() error = %v, wantErr %v", err, tc.expectedErr)
			}
			if sa != tc.expectedSA {
				t.Errorf("ServiceAccountName: got '%s', want '%s'", sa, tc.expectedSA)
			}
			if len(initContainers) != tc.expectedInitContainerCount {
				t.Errorf("InitContainers count: got %d, want %d", len(initContainers), tc.expectedInitContainerCount)
			}
			if len(containers) != tc.expectedContainerCount {
				t.Errorf("Containers count: got %d, want %d", len(containers), tc.expectedContainerCount)
			}
			if tc.expectContainersSorted && len(containers) > 0 {
				if containers[0].Name != tc.expectedFirstContainerName {
					t.Errorf("Expected containers to be sorted, first container name got '%s', want '%s'", containers[0].Name, tc.expectedFirstContainerName)
				}
			}
		})
	}
}

func TestJobDiff(t *testing.T) {
	logger := testLogger()
	r := &GenericReconciler{}

	// Define some containers for testing
	container1 := detailedContainerMap("c1", "image:v1", []string{"start"}, nil, nil, nil, nil, "")
	container2 := detailedContainerMap("c2", "image:v2", []string{"run"}, nil, nil, nil, nil, "")

	initContainer1 := detailedContainerMap("init1", "init:v1", []string{"setup"}, nil, nil, nil, nil, "")
	initContainer2 := detailedContainerMap("init1", "init:v1", []string{"setup", "extra"}, nil, nil, nil, nil, "") // Different args

	testCases := []struct {
		name        string
		existingJob *unstructured.Unstructured
		desiredJob  *unstructured.Unstructured
		expectDiff  bool
	}{
		{
			name:        "identical jobs",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1}, []map[string]interface{}{initContainer1}),
			desiredJob:  newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1}, []map[string]interface{}{initContainer1}),
			expectDiff:  false,
		},
		{
			name:        "different serviceAccountName",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1}, nil),
			desiredJob:  newUnstructuredJob(t, "test-job", "sa-2", []map[string]interface{}{container1}, nil),
			expectDiff:  true,
		},
		{
			name:        "different main container spec (args)",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1}, nil),
			desiredJob: newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{
				detailedContainerMap("c1", "image:v1", []string{"start", "--debug"}, nil, nil, nil, nil, ""), // New args
			}, nil),
			expectDiff: true,
		},
		{
			name:        "different number of main containers",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1}, nil),
			desiredJob:  newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1, container2}, nil),
			expectDiff:  true,
		},
		{
			name:        "different init container spec (args)",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", nil, []map[string]interface{}{initContainer1}),
			desiredJob:  newUnstructuredJob(t, "test-job", "sa-1", nil, []map[string]interface{}{initContainer2}),
			expectDiff:  true,
		},
		{
			name:        "different number of init containers",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", nil, []map[string]interface{}{initContainer1}),
			desiredJob:  newUnstructuredJob(t, "test-job", "sa-1", nil, []map[string]interface{}{initContainer1, initContainer2}),
			expectDiff:  true,
		},
		{
			name:        "same main containers but different order",
			existingJob: newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container1, container2}, nil),
			desiredJob:  newUnstructuredJob(t, "test-job", "sa-1", []map[string]interface{}{container2, container1}, nil),
			expectDiff:  false, // Should be false because getJobPodSpecDetails sorts them by name
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := r.jobDiff(tc.existingJob, tc.desiredJob, logger)
			if err != nil {
				t.Fatalf("jobDiff returned an unexpected error: %v", err)
			}
			if diff != tc.expectDiff {
				t.Errorf("jobDiff() = %v, want %v", diff, tc.expectDiff)
			}
		})
	}
}
