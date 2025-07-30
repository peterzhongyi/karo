package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// --- Test Suite for AgenticSandboxReconciler ---

func TestAgenticSandboxReconcileStateful(t *testing.T) {
	const namespace = "default"
	const sandboxName = "test-sandbox"
	var serviceIP = "10.0.0.1"    // Changed from const to var
	var servicePort = int32(8888) // Changed from const to var

	// 1. Define the different states our child resources can be in
	pendingDeployment := makeTestDeployment(sandboxName, namespace, false) // Not yet available
	readyDeployment := makeTestDeployment(sandboxName, namespace, true)    // Available
	sandboxService := makeTestService(sandboxName, namespace, serviceIP, servicePort)

	// 2. Define our test scenarios
	testCases := []struct {
		name               string
		inputSandbox       *unstructured.Unstructured
		initialObjects     []client.Object // For Deployments, Services
		expectedResult     ctrl.Result
		expectErr          bool
		expectedPhase      string
		expectIPInStatus   bool
		expectPortInStatus bool
		expectedIPValue    string
		expectedPortValue  int64
	}{
		{
			name:           "State: Initial, no children exist yet",
			inputSandbox:   makeTestSandbox(sandboxName, namespace, "", nil, nil),
			initialObjects: []client.Object{},
			expectedResult: ctrl.Result{Requeue: true},
			expectedPhase:  "Pending",
		},
		{
			name:           "State: Deployment exists but is not ready",
			inputSandbox:   makeTestSandbox(sandboxName, namespace, "Pending", nil, nil),
			initialObjects: []client.Object{pendingDeployment},
			expectedResult: ctrl.Result{RequeueAfter: 10 * time.Second},
			expectedPhase:  "Pending",
		},
		{
			name:               "State: Deployment is ready, Service exists",
			inputSandbox:       makeTestSandbox(sandboxName, namespace, "Pending", nil, nil),
			initialObjects:     []client.Object{readyDeployment, sandboxService},
			expectedResult:     ctrl.Result{},
			expectedPhase:      "Running",
			expectIPInStatus:   true,
			expectPortInStatus: true,
			expectedIPValue:    serviceIP,
			expectedPortValue:  int64(servicePort),
		},
		{
			name:               "State: Already Running (Idempotency check)",
			inputSandbox:       makeTestSandbox(sandboxName, namespace, "Running", &serviceIP, &servicePort),
			initialObjects:     []client.Object{readyDeployment, sandboxService},
			expectedResult:     ctrl.Result{},
			expectedPhase:      "Running", // Should not change
			expectIPInStatus:   true,      // Check that the IP is preserved
			expectPortInStatus: true,      // Check that the Port is preserved
			expectedIPValue:    serviceIP,
			expectedPortValue:  int64(servicePort),
		},
		{
			name:             "State: Deployment is ready, but Service is missing IP",
			inputSandbox:     makeTestSandbox(sandboxName, namespace, "Pending", nil, nil),
			initialObjects:   []client.Object{readyDeployment, makeTestService(sandboxName, namespace, "", servicePort)}, // Service without IP
			expectedResult:   ctrl.Result{},                                                                              // Finishes but IP won't be set
			expectedPhase:    "Running",
			expectIPInStatus: false, // IP should not be set
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := runtime.NewScheme()
			_ = appsv1.AddToScheme(s)
			_ = corev1.AddToScheme(s)

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(tc.initialObjects...).
				Build()

			mockGenericReconciler := &GenericReconciler{Client: fakeClient}
			sandboxReconciler := &AgenticSandboxReconciler{}

			result, err := sandboxReconciler.ReconcileStateful(context.Background(), mockGenericReconciler, tc.inputSandbox)

			// ASSERT
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.expectedResult, result)

			status, found, _ := unstructured.NestedMap(tc.inputSandbox.Object, "status")
			require.True(t, found)
			assert.Equal(t, tc.expectedPhase, status["phase"])

			if tc.expectIPInStatus {
				assert.Equal(t, tc.expectedIPValue, status["sandboxIP"])
			} else {
				assert.Nil(t, status["sandboxIP"])
			}

			if tc.expectPortInStatus {
				assert.Equal(t, tc.expectedPortValue, status["serverPort"])
			} else {
				assert.Nil(t, status["serverPort"])
			}
		})
	}
}

// --- Helper functions to create test objects ---

func makeTestSandbox(name, namespace, phase string, ip *string, port *int32) *unstructured.Unstructured {
	cr := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}
	cr.SetAPIVersion("model.skippy.io/v1")
	cr.SetKind("AgenticSandbox")
	cr.SetName(name)
	cr.SetNamespace(namespace)

	status := make(map[string]interface{})
	if phase != "" {
		status["phase"] = phase
	}
	if ip != nil {
		status["sandboxIP"] = *ip
	}
	if port != nil {
		status["serverPort"] = int64(*port)
	}

	if len(status) > 0 {
		unstructured.SetNestedMap(cr.Object, status, "status")
	}

	return cr
}

func makeTestDeployment(name, namespace string, isAvailable bool) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if isAvailable {
		dep.Status.Conditions = []appsv1.DeploymentCondition{
			{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue},
		}
	} else {
		dep.Status.Conditions = []appsv1.DeploymentCondition{
			{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionFalse},
		}
	}
	return dep
}

func makeTestService(name, namespace, clusterIP string, port int32) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: clusterIP,
			Ports: []corev1.ServicePort{
				{Port: port},
			},
		},
	}
}
