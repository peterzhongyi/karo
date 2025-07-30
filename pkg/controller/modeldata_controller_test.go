package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// --- Test Suite for ModelDataReconciler ---

func TestModelDataReconcileStateful(t *testing.T) {
	const namespace = "default"
	const modelDataName = "test-model-data"
	const jobName = "test-model-data"
	const gitHash = "abcdef12345"
	const finalGCSPath = "gs://my-bucket/my-prefix/abcdef12345"

	// Define a standard list of dependents for use in multiple tests
	dependentsList := []interface{}{
		map[string]interface{}{"kind": "Job", "name": jobName, "namespace": namespace},
	}

	// 1. Define the different states our Job and Pod can be in
	runningJob := makeTestJob(jobName, namespace, "Running")
	succeededJob := makeTestJob(jobName, namespace, "Succeeded")
	failedJob := makeTestJob(jobName, namespace, "Failed")
	podForSucceededJob := makeTestPod("job-pod-ok", namespace, jobName, gitHash)

	// 2. Define our test scenarios
	testCases := []struct {
		name                   string
		inputModelData         *unstructured.Unstructured
		initialObjects         []client.Object // For Jobs, Pods
		expectedResult         ctrl.Result
		expectErr              bool
		expectedPhase          string
		expectedMessage        string
		expectRevisionInStatus bool
	}{
		{
			name:            "State: Initial, no dependents in status",
			inputModelData:  makeTestModelData(modelDataName, namespace, "Pending", nil), // Correct: no dependents
			initialObjects:  []client.Object{},
			expectedResult:  ctrl.Result{RequeueAfter: 15 * time.Second},
			expectedPhase:   "Pending",
			expectedMessage: "Waiting for download job to be created.",
		},
		{
			name:            "State: Job is running",
			inputModelData:  makeTestModelData(modelDataName, namespace, "Pending", dependentsList), // FIX: Add dependents
			initialObjects:  []client.Object{runningJob},
			expectedResult:  ctrl.Result{RequeueAfter: 10 * time.Second},
			expectedPhase:   "Syncing",
			expectedMessage: "Model synchronization Job is in progress.",
		},
		{
			name:                   "State: Job Succeeded",
			inputModelData:         makeTestModelData(modelDataName, namespace, "Syncing", dependentsList),
			initialObjects:         []client.Object{succeededJob, podForSucceededJob},
			expectedResult:         ctrl.Result{},
			expectedPhase:          "Succeeded",
			expectedMessage:        "Model synchronization complete.",
			expectRevisionInStatus: true, // This tells the test to check the final path
		},
		{
			name:            "State: Job Failed",
			inputModelData:  makeTestModelData(modelDataName, namespace, "Syncing", dependentsList), // FIX: Add dependents
			initialObjects:  []client.Object{failedJob},
			expectedResult:  ctrl.Result{},
			expectedPhase:   "Failed",
			expectedMessage: "The model synchronization Job failed.",
		},
		{
			name:           "State: Already Succeeded (Idempotency check)",
			inputModelData: makeTestModelData(modelDataName, namespace, "Succeeded", nil), // Correct: No dependents needed
			initialObjects: []client.Object{},
			expectedResult: ctrl.Result{},
			expectErr:      false,
			expectedPhase:  "Succeeded", // Should not change phase
		},
		{
			name:            "Error State: Job succeeded but pod was garbage collected",
			inputModelData:  makeTestModelData(modelDataName, namespace, "Syncing", dependentsList), // FIX: Add dependents
			initialObjects:  []client.Object{succeededJob},                                          // Note: No pod
			expectedResult:  ctrl.Result{},
			expectErr:       true,
			expectedPhase:   "Failed",
			expectedMessage: "Job succeeded but could not read result",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// ARRANGE
			s := runtime.NewScheme()
			_ = batchv1.AddToScheme(s)
			_ = corev1.AddToScheme(s)

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(tc.initialObjects...).
				Build()

			mockGenericReconciler := &GenericReconciler{Client: fakeClient}
			modelDataReconciler := &ModelDataReconciler{}

			// ACT
			result, err := modelDataReconciler.ReconcileStateful(context.Background(), mockGenericReconciler, tc.inputModelData)

			// ASSERT
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.expectedResult, result)

			status, found, _ := unstructured.NestedMap(tc.inputModelData.Object, "status")
			require.True(t, found)
			assert.Equal(t, tc.expectedPhase, status["phase"])

			if tc.expectedMessage != "" {
				assert.Contains(t, status["message"], tc.expectedMessage)
			}

			if tc.expectRevisionInStatus {
				assert.Equal(t, gitHash, status["resolvedRevision"])
				assert.Equal(t, finalGCSPath, status["finalGcsPath"])
			}
		})
	}
}

// --- Helper functions to create test objects ---

func makeTestModelData(name, namespace, phase string, dependents []interface{}) *unstructured.Unstructured {
	cr := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}
	cr.SetAPIVersion("model.skippy.io/v1")
	cr.SetKind("ModelData")
	cr.SetName(name)
	cr.SetNamespace(namespace)

	// FIX: Add the prefix to the test data so the helper function can build the path
	unstructured.SetNestedField(cr.Object, "gs://my-bucket", "spec", "destination", "gcsBucket")
	unstructured.SetNestedField(cr.Object, "my-prefix", "spec", "destination", "prefix")

	status := make(map[string]interface{})
	if phase != "" {
		status["phase"] = phase
	}
	if dependents != nil {
		status["dependentResources"] = dependents
	}

	if len(status) > 0 {
		unstructured.SetNestedMap(cr.Object, status, "status")
	}

	return cr
}

func makeTestJob(name, namespace, phase string) *batchv1.Job {
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"job-name": name},
		},
		Spec: batchv1.JobSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"job-name": name},
			},
		},
	}
	switch phase {
	case "Succeeded":
		job.Status.Succeeded = 1
		job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	case "Failed":
		job.Status.Failed = 1
		job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
	case "Running":
		job.Status.Active = 1
	}
	return job
}

func makeTestPod(name, namespace, jobName, termMsg string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "gcloud-upload",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Message: termMsg,
						},
					},
				},
			},
		},
	}
}
