package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
)

// KindReconciler defines the interface for kind-specific reconciliation logic.
type KindReconciler interface {
	// ReconcileStateful is responsible for managing the lifecycle and status
	// of a resource that has a state machine (like a Job).
	ReconcileStateful(ctx context.Context, r *GenericReconciler, obj *unstructured.Unstructured) (ctrl.Result, error)
}
