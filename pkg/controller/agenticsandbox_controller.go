package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// AgenticSandboxReconciler implements the stateful logic for AgenticSandbox CRs.
type AgenticSandboxReconciler struct{}

// ReconcileStateful contains the state machine logic for an AgenticSandbox.
func (asr *AgenticSandboxReconciler) ReconcileStateful(ctx context.Context, r *GenericReconciler, sandbox *unstructured.Unstructured) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("AgenticSandbox.Name", sandbox.GetName())

	// 1. Get the current status from the sandbox object.
	status, statusFound, _ := unstructured.NestedMap(sandbox.Object, "status")
	if !statusFound {
		// This is the very first reconciliation for this object.
		// The generic reconciler hasn't created the children yet.
		// Set phase to Pending and requeue.
		asr.updateStatusFields(sandbox, "Pending", nil, nil)
		return ctrl.Result{Requeue: true}, nil
	}

	// 2. Check if the sandbox is already in a terminal "Running" state.
	phase, _, _ := unstructured.NestedString(status, "phase")
	if phase == "Running" {
		// The sandbox is up and running with its IP and port set.
		// Our work here is done, no need to requeue unless the object changes.
		return ctrl.Result{}, nil
	}

	// 3. Fetch the child Deployment to check its readiness.
	// The child Deployment has the same name and namespace as the sandbox CR.
	deployment := &appsv1.Deployment{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: sandbox.GetName(), Namespace: sandbox.GetNamespace()}, deployment)
	if err != nil {
		if errors.IsNotFound(err) {
			// The Deployment hasn't been created yet by the generic reconciler.
			logger.Info("Waiting for child Deployment to be created.")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		logger.Error(err, "Failed to get child Deployment.")
		return ctrl.Result{}, err
	}

	// 4. Determine the phase based on the Deployment's availability.
	deploymentIsAvailable := false
	for _, cond := range deployment.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			deploymentIsAvailable = true
			break
		}
	}

	if !deploymentIsAvailable {
		// The Deployment exists but is not yet fully available.
		logger.Info("Child Deployment is not yet available, requeueing.")
		asr.updateStatusFields(sandbox, "Pending", nil, nil)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 5. Fetch the child Service to get its ClusterIP and Port.
	service := &corev1.Service{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: sandbox.GetName(), Namespace: sandbox.GetNamespace()}, service)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Waiting for child Service to be created.")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		logger.Error(err, "Failed to get child Service.")
		return ctrl.Result{}, err
	}

	// 6. The Deployment is ready and the Service exists. Update status to "Running".
	logger.Info("Child Deployment is available. Updating status to Running.")
	asr.updateStatusFields(sandbox, "Running", service, deployment)

	// Reconciliation is complete and successful.
	return ctrl.Result{}, nil
}

// updateStatusFields modifies the AgenticSandbox object in memory with the correct phase and connection details.
func (asr *AgenticSandboxReconciler) updateStatusFields(sandbox *unstructured.Unstructured, phase string, service *corev1.Service, deployment *appsv1.Deployment) error {
	status, _, _ := unstructured.NestedMap(sandbox.Object, "status")
	if status == nil {
		status = make(map[string]interface{})
	}

	status["phase"] = phase

	if service != nil && service.Spec.ClusterIP != "" {
		status["sandboxIP"] = service.Spec.ClusterIP
		status["serverPort"] = int64(service.Spec.Ports[0].Port)
	} else {
		// Clear fields if the service isn't ready
		delete(status, "sandboxIP")
		delete(status, "serverPort")
	}

	return unstructured.SetNestedMap(sandbox.Object, status, "status")
}
