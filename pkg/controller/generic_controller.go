// controller/generic_reconciler.go
package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"github.com/go-logr/logr"
)

const (
	ReadyConditionType                  = "Ready"
	ReconciliationFailedReason          = "ReconciliationFailed"
	ReconciliationSucceededReason       = "ReconciliationSucceeded"
	SetOwnerRefFailedEvent              = "SetOwnerRefFailed"
	OwnerDeletedDuringStatusUpdateEvent = "OwnerDeletedDuringStatusUpdate"
	StatusUpdateFailedEvent             = "StatusUpdateFailed"
	StatusUpdatedEvent                  = "StatusUpdated"
	TransformerRunFailedEvent           = "TransformerRunFailed"
	UnsupportedDependentKindEvent       = "UnsupportedDependentKind"
	DependentUpdateFailedEvent          = "DependentUpdateFailed"
	DependentCreateFailedEvent          = "DependentCreateFailed"
	DependentUpdatedEvent               = "DependentUpdated"
	DependentCreatedEvent               = "DependentCreated"
	ReconciliationSuccessfulEvent       = "ReconciliationSuccessful"
	DependentUpdateStartedEvent         = "DependentUpdateStarted"
	DependentCreateStartedEvent         = "DependentCreateStarted"
)

type GenericReconciler struct {
	Mutex                  *sync.Mutex
	Client                 client.Client
	Scheme                 *runtime.Scheme
	Transformer            modelv1.TransformerInterface // Use the interface
	Gvk                    schema.GroupVersionKind
	Recorder               record.EventRecorder
	resourceClientFactory  func(dynamic.Interface) modelv1.ResourceClientInterface
	discoveryClientFactory func() (discovery.DiscoveryInterface, error)
	getResourceReconciler  func(kind string) (*ResourceReconciler, error)
	KindReconcilers        map[string]KindReconciler
}

type ResourceClient struct {
	dynClient dynamic.Interface
}

// SimplifiedContainerSpec holds only the fields we care about for comparison
type SimplifiedContainerSpec struct {
	Name         string
	Args         []string
	Env          []corev1.EnvVar
	VolumeMounts []corev1.VolumeMount
	Resources    corev1.ResourceRequirements
}

type DiffFunc func(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error)

type ResourceReconciler struct {
	diffFunc DiffFunc
}

func (rc *ResourceClient) Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	resourceName := strings.ToLower(gvk.Kind) + "s"
	resource := rc.dynClient.Resource(gvk.GroupVersion().WithResource(resourceName))
	return resource.Namespace(namespace).Get(ctx, name, v1.GetOptions{})
}

func (rc *ResourceClient) Create(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	resourceName := strings.ToLower(gvk.Kind) + "s"
	resource := rc.dynClient.Resource(gvk.GroupVersion().WithResource(resourceName))
	return resource.Namespace(namespace).Create(ctx, obj, v1.CreateOptions{})
}

func (rc *ResourceClient) Update(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	resourceName := strings.ToLower(gvk.Kind) + "s"
	resource := rc.dynClient.Resource(gvk.GroupVersion().WithResource(resourceName))
	return resource.Namespace(namespace).Update(ctx, obj, v1.UpdateOptions{})
}

func (r *GenericReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// createEmptyObject() should return an *unstructured.Unstructured
	// with the GVK set to r.Gvk
	objectToWatch := r.createEmptyObject()
	if objectToWatch == nil {
		err := fmt.Errorf("createEmptyObject returned nil for GVK %v", r.Gvk)
		return err
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(objectToWatch). // Watch for the GVK defined in this GenericReconciler
		Complete(r)         // This GenericReconciler's Reconcile method will be called

	return err
}

func (r *GenericReconciler) createEmptyObject() *unstructured.Unstructured {
	target := &unstructured.Unstructured{}
	target.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   r.Gvk.Group,
		Version: r.Gvk.Version,
		Kind:    r.Gvk.Kind,
	})
	return target
}

func (r *GenericReconciler) fetchTarget(ctx context.Context, req ctrl.Request) (*unstructured.Unstructured, error) {
	target := r.createEmptyObject()
	if err := r.Client.Get(ctx, req.NamespacedName, target); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("resource not found: %w", err)
		}
		return nil, fmt.Errorf("failed to fetch resource: %w", err)
	}
	return target, nil
}

func (r *GenericReconciler) setupClients(ctx context.Context) (discovery.DiscoveryInterface, dynamic.Interface, error) {
	discoveryClient, err := r.discoveryClientFactory()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create discovery client: %w", err)
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create dynamic client: %w", err)
	}
	return discoveryClient, dynamicClient, nil
}

func (r *GenericReconciler) processDependentResources(
	ctx context.Context,
	log logr.Logger,
	target *unstructured.Unstructured,
	objs []*unstructured.Unstructured,
	resourceClient modelv1.ResourceClientInterface,
) ([]map[string]interface{}, error) {
	var processedResources []map[string]interface{}
	var firstError error

	for _, obj := range objs {
		info, err := r.processSingleDependentResource(ctx, log, target, obj, resourceClient)
		if err != nil && firstError == nil {
			firstError = err
		}
		processedResources = append(processedResources, info)
	}
	return processedResources, firstError
}

func (r *GenericReconciler) processSingleDependentResource(
	ctx context.Context,
	log logr.Logger,
	target *unstructured.Unstructured,
	obj *unstructured.Unstructured,
	resourceClient modelv1.ResourceClientInterface,
) (map[string]interface{}, error) {
	dependentResourceInfo := map[string]interface{}{
		"kind":      obj.GetKind(),
		"name":      obj.GetName(),
		"namespace": obj.GetNamespace(),
		"status":    "Attempted",
	}

	// This is the function we suspect is failing.
	err := controllerutil.SetControllerReference(target, obj, r.Scheme)
	if err != nil {
		log.Error(err, "Failed to set ControllerReference")
		r.Recorder.Eventf(target, corev1.EventTypeWarning, SetOwnerRefFailedEvent, "Failed to set owner ref on %s %s for %s %s: %v", obj.GetKind(), obj.GetName(), target.GetKind(), target.GetName(), err)
		dependentResourceInfo["status"] = fmt.Sprintf("Error: SetOwnerRefFailed - %v", err)
		return dependentResourceInfo, fmt.Errorf("failed to set controller reference: %w", err)
	}

	finalProcessedObj, err := r.reconcileResource(ctx, log, resourceClient, target, obj)
	if err != nil {
		dependentResourceInfo["status"] = fmt.Sprintf("Error: %v", err)
		return dependentResourceInfo, fmt.Errorf("failed to reconcile resource: %w", err)
	}
	dependentResourceInfo["status"] = "Processed"
	if finalProcessedObj != nil && finalProcessedObj.GetUID() != "" {
		dependentResourceInfo["uid"] = string(finalProcessedObj.GetUID())
	}
	return dependentResourceInfo, nil
}

func (r *GenericReconciler) updateStatus(ctx context.Context, log logr.Logger, originalTarget *unstructured.Unstructured, target *unstructured.Unstructured, processedDependentResources []map[string]interface{}, overallReconciliationFailed bool, reconciliationErr error) error {
	statusTarget := target.DeepCopy()
	unstructured.SetNestedField(statusTarget.Object, target.GetGeneration(), "status", "observedGeneration")

	newConditions, err := r.buildConditions(ctx, target, overallReconciliationFailed, reconciliationErr)
	if err != nil {
		log.Error(err, "Failed to build conditions")
		return fmt.Errorf("failed to build conditions: %w", err)
	}

	if err := unstructured.SetNestedField(statusTarget.Object, newConditions, "status", "conditions"); err != nil {
		log.Error(err, "Failed to set conditions in status")
		return fmt.Errorf("failed to set conditions in status: %w", err)
	}

	dependentResourcesAsInterfaceSlice := make([]interface{}, len(processedDependentResources))
	for i, v := range processedDependentResources {
		dependentResourcesAsInterfaceSlice[i] = v
	}
	if err := unstructured.SetNestedField(statusTarget.Object, dependentResourcesAsInterfaceSlice, "status", "dependentResources"); err != nil {
		log.Error(err, "Failed to set dependentResources in status")
		return fmt.Errorf("failed to set dependentResources in status: %w", err)
	}

	if err := unstructured.SetNestedField(statusTarget.Object, int64(len(processedDependentResources)), "status", "createdResourceCount"); err != nil {
		log.Error(err, "Failed to set createdResourceCount in status")
		return fmt.Errorf("failed to set createdResourceCount in status: %w", err)
	}
	originalTargetStatus, statusFound, _ := unstructured.NestedMap(originalTarget.Object, "status")

	newStatusMap, _, _ := unstructured.NestedMap(statusTarget.Object, "status")
	if !statusFound || !reflect.DeepEqual(originalTargetStatus, newStatusMap) {
		if err := r.Client.Status().Update(ctx, statusTarget); err != nil {
			if errors.IsNotFound(err) {
				log.Info("Owner resource not found during status update attempt, likely deleted. Not re-queuing.")
				if r.Recorder != nil {
					r.Recorder.Eventf(target, corev1.EventTypeWarning, OwnerDeletedDuringStatusUpdateEvent, "Owner %s %s was deleted before status could be updated.", target.GetKind(), target.GetName())
				}
				return nil // Return nil, because the owner is gone, no need to requeue
			}
			log.Error(err, "Failed to update target status subresource")
			if r.Recorder != nil {
				r.Recorder.Eventf(target, corev1.EventTypeWarning, StatusUpdateFailedEvent, "Failed to update status for %s %s: %v", target.GetKind(), target.GetName(), err)
			}
			return fmt.Errorf("failed to update target status subresource: %w", err) // Requeue for other errors
		}
		log.Info("Successfully updated target status", "generation", target.GetGeneration(), "observedGeneration", target.GetGeneration())
		if r.Recorder != nil {
			r.Recorder.Eventf(target, corev1.EventTypeNormal, StatusUpdatedEvent, "Status updated for %s %s", target.GetKind(), target.GetName())
		}
	} else {
		log.Info("Target status is already up-to-date.")
	}
	return nil
}

func (r *GenericReconciler) buildConditions(ctx context.Context, target *unstructured.Unstructured, overallReconciliationFailed bool, reconciliationErr error) ([]interface{}, error) {
	var newConditions []interface{}
	existingConditionsRaw, _, _ := unstructured.NestedSlice(target.Object, "status", "conditions")
	existingConditions := []v1.Condition{}
	for _, rawCond := range existingConditionsRaw {
		condMap, ok := rawCond.(map[string]interface{})
		if !ok {
			continue
		}

		// Safely parse lastTransitionTime
		var lastTransitionTime v1.Time
		if tStr, ok := condMap["lastTransitionTime"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, tStr); err == nil {
				lastTransitionTime = v1.NewTime(t)
			} else {
				// Handle parse error or default
				lastTransitionTime = v1.Now()
			}
		}

		// Safely get observedGeneration
		var observedGen int64
		if obsGenVal, ok := condMap["observedGeneration"]; ok {
			if val, isInt64 := obsGenVal.(int64); isInt64 {
				observedGen = val
			}
		}

		existingConditions = append(existingConditions, v1.Condition{
			Type:               getStringValue(condMap, "type"), // Using a helper is safer
			Status:             v1.ConditionStatus(getStringValue(condMap, "status")),
			Reason:             getStringValue(condMap, "reason"),
			Message:            getStringValue(condMap, "message"),
			LastTransitionTime: lastTransitionTime,
			ObservedGeneration: observedGen, // Use the safely extracted value
		})
	}

	desiredReadyCondition := v1.Condition{
		Type:               ReadyConditionType,
		ObservedGeneration: target.GetGeneration(),
	}

	if overallReconciliationFailed || reconciliationErr != nil {
		desiredReadyCondition.Status = v1.ConditionFalse
		desiredReadyCondition.Reason = ReconciliationFailedReason
		if reconciliationErr != nil {
			desiredReadyCondition.Message = fmt.Sprintf("Failed to reconcile: %v", reconciliationErr)
		} else {
			desiredReadyCondition.Message = "One or more dependent resources failed to reconcile."
		}
	} else {
		desiredReadyCondition.Status = v1.ConditionTrue
		desiredReadyCondition.Reason = ReconciliationSucceededReason
		desiredReadyCondition.Message = "All dependent resources successfully processed."
	}

	foundReadyCondition := false
	for i, cond := range existingConditions {
		if cond.Type == ReadyConditionType {
			foundReadyCondition = true
			if cond.Status != desiredReadyCondition.Status ||
				cond.Reason != desiredReadyCondition.Reason ||
				cond.Message != desiredReadyCondition.Message ||
				cond.ObservedGeneration != desiredReadyCondition.ObservedGeneration {
				if cond.Status != desiredReadyCondition.Status {
					desiredReadyCondition.LastTransitionTime = v1.Now()
				} else {
					desiredReadyCondition.LastTransitionTime = cond.LastTransitionTime
				}
				existingConditions[i] = desiredReadyCondition
			} else {
				desiredReadyCondition = cond
			}
			break
		}
	}

	if !foundReadyCondition {
		desiredReadyCondition.LastTransitionTime = v1.Now()
		existingConditions = append(existingConditions, desiredReadyCondition)
	}
	newConditions = make([]interface{}, len(existingConditions))
	for i, cond := range existingConditions {
		newConditions[i] = map[string]interface{}{
			"type":               cond.Type,
			"status":             string(cond.Status),
			"lastTransitionTime": cond.LastTransitionTime.Format(time.RFC3339Nano),
			"reason":             cond.Reason,
			"message":            cond.Message,
			"observedGeneration": cond.ObservedGeneration,
		}
	}
	return newConditions, nil
}

func (r *GenericReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hasIntegration := r.Transformer.Registry().HasIntegration(r.Gvk)
	if !hasIntegration {
		return ctrl.Result{Requeue: false}, nil
	}

	r.Mutex.Lock()
	defer r.Mutex.Unlock()

	log := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name, "controller", r.Gvk.Kind)
	log.Info("reconciling resource")

	target, err := r.fetchTarget(ctx, req)
	if err != nil {
		if strings.Contains(err.Error(), "resource not found") {
			log.Info("resource not found")
			return ctrl.Result{}, nil
		}
		log.Error(err, "failed to fetch target resource")
		return ctrl.Result{}, err
	}

	originalTarget := target.DeepCopy()

	discoveryClient, dynamicClient, err := r.setupClients(ctx)
	if err != nil {
		log.Error(err, "failed to setup clients")
		return ctrl.Result{}, err
	}

	// Type assertion to get the concrete *dynamic.DynamicClient
	dynClient, ok := dynamicClient.(*dynamic.DynamicClient)
	if !ok {
		log.Error(fmt.Errorf("failed to assert dynamicClient to *dynamic.DynamicClient, this should not happen"), "unexpected type")
		return ctrl.Result{}, fmt.Errorf("unexpected type for dynamicClient, this should not happen")
	}

	resourceClient := r.resourceClientFactory(dynamicClient)

	mapper := r.Client.RESTMapper()

	var reconciliationErr error
	var overallReconciliationFailed bool

	objs, err := r.Transformer.Run(ctx, discoveryClient, dynClient, mapper, r.Client, req, target)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(target, corev1.EventTypeWarning, TransformerRunFailedEvent, "Failed to generate desired state for %s %s: %v", target.GetKind(), target.GetName(), err)
		}
		reconciliationErr = err
		overallReconciliationFailed = true
	}
	var processedDependentResources []map[string]interface{}
	if objs != nil {
		processedDependentResources, reconciliationErr = r.processDependentResources(ctx, log, target, objs, resourceClient)
		if reconciliationErr != nil {
			overallReconciliationFailed = true
		}
	}

	if kindReconciler, ok := r.KindReconcilers[target.GetKind()]; ok {
		result, err := kindReconciler.ReconcileStateful(ctx, r, target)
		if err != nil {
			// A real error occurred in the stateful logic
			r.updateStatus(ctx, log, originalTarget, target, processedDependentResources, true, err)
			return ctrl.Result{}, err
		}
		if !result.IsZero() {
			// The stateful logic is waiting (requeuing). Update status and return.
			r.updateStatus(ctx, log, originalTarget, target, processedDependentResources, false, nil)
			return result, nil
		}
	}

	if !target.GetDeletionTimestamp().IsZero() {
		log.Info("Owner resource is being deleted, skipping status update",
			"targetKind", target.GetKind(), "targetName", target.GetName())

		if reconciliationErr != nil {
			return ctrl.Result{}, reconciliationErr
		}
		return ctrl.Result{}, nil
	}

	err = r.updateStatus(ctx, log, originalTarget, target, processedDependentResources, overallReconciliationFailed, reconciliationErr)
	if err != nil {
		log.Error(err, "failed to update status")
		if reconciliationErr == nil {
			reconciliationErr = err
		}
		return ctrl.Result{Requeue: true}, reconciliationErr
	}
	if reconciliationErr != nil {
		return ctrl.Result{}, reconciliationErr
	}
	r.Recorder.Eventf(target, corev1.EventTypeNormal, ReconciliationSuccessfulEvent, "All dependent resources processed successfully for %s %s", target.GetKind(), target.GetName())
	return ctrl.Result{Requeue: false, RequeueAfter: 5 * time.Second}, nil
}

func (r *GenericReconciler) reconcileResource(ctx context.Context, log logr.Logger, rc modelv1.ResourceClientInterface, target *unstructured.Unstructured, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	gvk := obj.GroupVersionKind()
	namespace := obj.GetNamespace()
	name := obj.GetName()

	log.Info("Processing resource for reconciliation logic", "gvk", gvk, "resourceName", name, "namespace", namespace)

	existingObj, err := rc.Get(ctx, gvk, namespace, name)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Error during Get call for existing object", "GVK", gvk, "namespace", namespace, "name", name)
		return nil, fmt.Errorf("error getting resource %s %s/%s: %w", gvk.String(), namespace, name, err)
	}

	var resourceReconciler *ResourceReconciler
	if r.getResourceReconciler != nil {
		// Use the override from the field if it exists (for tests).
		resourceReconciler, err = r.getResourceReconciler(gvk.Kind)
	} else {
		// Otherwise, use the default production logic.
		resourceReconciler, err = r.defaultGetResourceReconciler(gvk.Kind)
	}
	if err != nil {
		log.Info("Unsupported resource type for specific reconcile logic", "resourceGVK", gvk.String())
		r.Recorder.Eventf(target, corev1.EventTypeWarning, UnsupportedDependentKindEvent, "Skipping unsupported dependent kind %s %s/%s for %s %s", gvk.Kind, namespace, name, target.GetKind(), target.GetName())
		return obj, nil
	}

	return r.reconcileGeneric(ctx, log, rc, target, namespace, existingObj, obj, obj.GetName(), gvk, resourceReconciler.diffFunc)
}

func (r *GenericReconciler) defaultGetResourceReconciler(kind string) (*ResourceReconciler, error) {
	switch kind {
	case "Deployment":
		return &ResourceReconciler{diffFunc: r.deploymentDiff}, nil
	case "Service":
		return &ResourceReconciler{diffFunc: r.serviceDiff}, nil
	case "Secret":
		return &ResourceReconciler{diffFunc: r.secretDiff}, nil
	case "ConfigMap":
		return &ResourceReconciler{diffFunc: r.configMapDiff}, nil
	case "Job":
		return &ResourceReconciler{diffFunc: r.jobDiff}, nil
	case "HorizontalPodAutoscaler":
		return &ResourceReconciler{diffFunc: r.hpaDiff}, nil
	case "PodMonitoring":
		return &ResourceReconciler{diffFunc: r.podMonitoringDiff}, nil
	default:
		return nil, fmt.Errorf("unsupported resource kind: %s", kind)
	}
}

func (r *GenericReconciler) createOrUpdateResource(ctx context.Context, log logr.Logger, rc modelv1.ResourceClientInterface, target *unstructured.Unstructured, obj *unstructured.Unstructured, gvk schema.GroupVersionKind, namespace string, resourceName string, existingObj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if existingObj != nil {
		r.Recorder.Eventf(target, corev1.EventTypeNormal, DependentUpdateStartedEvent, "Starting update of %s %s/%s for %s %s", obj.GetKind(), namespace, resourceName, target.GetKind(), target.GetName())
		obj.SetResourceVersion(existingObj.GetResourceVersion())
		updatedObj, err := rc.Update(ctx, gvk, namespace, obj)
		if err != nil {
			log.Error(err, "Error during Update call", "GVK", gvk, "Namespace", namespace, "Name", resourceName)
			r.Recorder.Eventf(target, corev1.EventTypeWarning, DependentUpdateFailedEvent, "Failed to update %s %s/%s for %s %s: %v", obj.GetKind(), namespace, resourceName, target.GetKind(), target.GetName(), err)
			return nil, fmt.Errorf("error updating resource %s %s/%s: %w", gvk.String(), namespace, resourceName, err)
		}
		log.Info("Resource updated", "GVK", gvk, "name", updatedObj.GetName(), "namespace", namespace)
		r.Recorder.Eventf(target, corev1.EventTypeNormal, DependentUpdatedEvent, "Successfully updated %s %s/%s for %s %s", updatedObj.GetKind(), namespace, updatedObj.GetName(), target.GetKind(), target.GetName())
		return updatedObj, nil
	} else {
		createdObj, err := rc.Create(ctx, gvk, namespace, obj)
		if err != nil {
			log.Error(err, "Error during Create call", "GVK", gvk, "Namespace", namespace, "Name", resourceName)
			r.Recorder.Eventf(target, corev1.EventTypeWarning, DependentCreateFailedEvent, "Failed to create %s %s/%s for %s %s: %v (%s)", obj.GetKind(), namespace, resourceName, target.GetKind(), target.GetName(), err, err.Error())
			return nil, fmt.Errorf("error creating resource %s %s/%s: %w", gvk.String(), namespace, resourceName, err)
		}
		log.Info("Resource created", "GVK", gvk, "name", createdObj.GetName(), "namespace", namespace)
		r.Recorder.Eventf(target, corev1.EventTypeNormal, DependentCreatedEvent, "Successfully created %s %s/%s (UID: %s) for %s %s", createdObj.GetKind(), createdObj.GetNamespace(), createdObj.GetName(), createdObj.GetUID(), target.GetKind(), target.GetName())
		return createdObj, nil
	}
	// Added this return nil,nil to satisfy compiler since the update path was omitted for brevity
	//return nil, nil
}

func (r *GenericReconciler) reconcileGeneric(
	ctx context.Context,
	log logr.Logger,
	rc modelv1.ResourceClientInterface,
	target *unstructured.Unstructured,
	namespace string,
	existingObj *unstructured.Unstructured,
	obj *unstructured.Unstructured,
	resourceName string,
	gvk schema.GroupVersionKind,
	diffFunc DiffFunc,
) (*unstructured.Unstructured, error) {
	canRecordEvent := r.Recorder != nil
	if existingObj != nil {
		hasSpecOrDataDiff, err := diffFunc(existingObj, obj, log)
		if err != nil {
			log.Error(err, "Error during diff check", "GVK", gvk, "Namespace", namespace, "Name", resourceName)
			if canRecordEvent {
				r.Recorder.Eventf(target, corev1.EventTypeWarning, "DiffCheckFailed", "Failed to compare desired state for dependent %s %s/%s: %v", obj.GetKind(), namespace, resourceName, err)
			}
			return nil, fmt.Errorf("error during diff for %s %s/%s: %w", gvk.String(), namespace, resourceName, err)
		}

		needsUpdateForOwnerRef := false
		desiredControllerRef := v1.GetControllerOf(obj)
		if desiredControllerRef != nil {
			currentControllerRefOnExisting := v1.GetControllerOf(existingObj)
			if currentControllerRefOnExisting == nil ||
				currentControllerRefOnExisting.APIVersion != desiredControllerRef.APIVersion ||
				currentControllerRefOnExisting.Kind != desiredControllerRef.Kind ||
				currentControllerRefOnExisting.UID != desiredControllerRef.UID {
				needsUpdateForOwnerRef = true
				log.Info("Resource needs update to set/correct owner reference",
					"GVK", gvk, "Namespace", namespace, "Name", resourceName,
					"desiredOwnerUID", desiredControllerRef.UID,
					"existingController", fmt.Sprintf("%v", currentControllerRefOnExisting))
			}
		}

		if hasSpecOrDataDiff || needsUpdateForOwnerRef {
			log.Info("Resource requires update",
				"GVK", gvk, "Namespace", namespace, "Name", resourceName,
				"hasSpecOrDataDiff", hasSpecOrDataDiff,
				"needsUpdateForOwnerRef", needsUpdateForOwnerRef)
			return r.createOrUpdateResource(ctx, log, rc, target, obj, gvk, namespace, resourceName, existingObj)
		} else {
			log.Info("Resource is the same, no update needed", "GVK", gvk, "name", resourceName, "namespace", namespace)
			return existingObj, nil
		}
	} else {
		log.Info("Resource does not exist, attempting to create", "GVK", gvk, "Namespace", namespace, "Name", resourceName)
		return r.createOrUpdateResource(ctx, log, rc, target, obj, gvk, namespace, resourceName, existingObj)
	}
}
