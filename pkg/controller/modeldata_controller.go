package controller

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ModelDataReconciler implements the stateful logic for ModelData CRs.
type ModelDataReconciler struct{}

// ReconcileStateful contains the state machine logic with added debugging.
func (m *ModelDataReconciler) ReconcileStateful(ctx context.Context, r *GenericReconciler, modelData *unstructured.Unstructured) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ModelData.Name", modelData.GetName())

	// 1. Get the entire status map safely.
	status, statusFound, _ := unstructured.NestedMap(modelData.Object, "status")
	if !statusFound {
		// If the status block doesn't exist at all, it's the first run.
		// Set to Pending and requeue so the main reconciler can create the Job.
		m.updateStatusFields(modelData, "Pending", "Waiting for download job to be created.", "", "")
		return ctrl.Result{Requeue: true}, nil
	}

	// 2. Check the phase from the status map we already fetched.
	phase, phaseFound, _ := unstructured.NestedString(status, "phase")
	if phaseFound && (phase == "Succeeded" || phase == "Failed") {
		// It's already done. Do nothing. This is our idempotency check.
		logger.Info("ModelData is already in a terminal state. No further action needed.", "phase", phase)
		return ctrl.Result{}, nil
	}
	// 1. Get the Job's name from the ModelData's status.
	dependents, found, _ := unstructured.NestedSlice(modelData.Object, "status", "dependentResources")
	if !found || len(dependents) == 0 {
		m.updateStatusFields(modelData, "Pending", "Waiting for download job to be created.", "", "")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}
	jobInfo, _ := dependents[0].(map[string]interface{})
	jobName, _ := jobInfo["name"].(string)

	// 2. Get the Job from the cluster.
	foundJob := &batchv1.Job{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelData.GetNamespace()}, foundJob)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		return ctrl.Result{}, err // Return real error
	}

	// 3. Determine the phase based on Job Status.
	isComplete := false
	for _, c := range foundJob.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			isComplete = true
			break
		}
	}

	isFailed := false
	for _, c := range foundJob.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			isFailed = true
			break
		}
	}

	if isFailed {
		m.updateStatusFields(modelData, "Failed", "The model synchronization Job failed.", "", "")
		return ctrl.Result{}, nil // Stop reconciling
	}

	if isComplete {
		gitHash, err := m.getHashFromTerminatedPod(ctx, r.Client, foundJob)
		if err != nil {
			m.updateStatusFields(modelData, "Failed", "Job succeeded but could not read result: "+err.Error(), "", "")
			return ctrl.Result{}, err
		}

		finalGCSPath := m.buildFinalGCSPath(modelData, gitHash)
		m.updateStatusFields(modelData, "Succeeded", "Model synchronization complete.", gitHash, finalGCSPath)
		return ctrl.Result{}, nil // Reconciliation is complete and successful.
	}

	// If not complete and not failed, it must be running or pending.
	m.updateStatusFields(modelData, "Syncing", "Model synchronization Job is in progress.", "", "")
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// updateStatusFields modifies the ModelData object in memory.
func (m *ModelDataReconciler) updateStatusFields(modelData *unstructured.Unstructured, phase, message, gitHash, gcsPath string) error {
	status, _, _ := unstructured.NestedMap(modelData.Object, "status")
	if status == nil {
		status = make(map[string]interface{})
	}

	status["phase"] = phase
	status["message"] = message
	if gitHash != "" {
		status["resolvedRevision"] = gitHash
		status["finalGcsPath"] = gcsPath
		status["lastSyncTime"] = metav1.Now().Format(time.RFC3339)
	}

	return unstructured.SetNestedMap(modelData.Object, status, "status")
}

// getHashFromTerminatedPod finds the completed pod for a job and reads its termination message.
func (m *ModelDataReconciler) getHashFromTerminatedPod(ctx context.Context, c client.Client, job *batchv1.Job) (string, error) {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(job.GetNamespace()), client.MatchingLabels(job.Spec.Selector.MatchLabels)); err != nil {
		return "", fmt.Errorf("failed to list pods for job %q: %w", job.GetName(), err)
	}
	if len(podList.Items) == 0 {
		return "", fmt.Errorf("no pods found for completed job %q", job.GetName())
	}
	pod := podList.Items[0]
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Name == "gcloud-upload" && containerStatus.State.Terminated != nil {
			return strings.TrimSpace(containerStatus.State.Terminated.Message), nil
		}
	}
	return "", fmt.Errorf("job %q finished but could not find termination message", job.GetName())
}

// buildFinalGCSPath robustly constructs the final GCS path string.
func (m *ModelDataReconciler) buildFinalGCSPath(modelData *unstructured.Unstructured, gitHash string) string {
	bucket, _, _ := unstructured.NestedString(modelData.Object, "spec", "destination", "gcsBucket")
	prefix, _, _ := unstructured.NestedString(modelData.Object, "spec", "destination", "prefix")

	// Clean up the bucket name to ensure it's consistent
	cleanBucket := strings.TrimPrefix(bucket, "gs://")
	cleanBucket = strings.TrimSuffix(cleanBucket, "/")

	// Use path.Join to intelligently join the path components without extra slashes
	fullPath := path.Join(prefix, gitHash)

	// Return the final path, ensuring it ends with a slash to denote a directory
	return fmt.Sprintf("gs://%s/%s", cleanBucket, fullPath)
}
