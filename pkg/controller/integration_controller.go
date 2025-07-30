package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"github.com/GoogleCloudPlatform/karo/pkg/transformer"
	"github.com/go-logr/logr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

// IntegrationReconciler reconciles a Integration object
type IntegrationReconciler struct {
	client.Client
	Manager     ctrl.Manager
	Transformer modelv1.TransformerInterface
	Scheme      *runtime.Scheme

	m            sync.Mutex
	genericMutex sync.Mutex
	reconcilers  map[string]*GenericReconciler

	setupGenericReconcilerFunc func(r *GenericReconciler) error

	KindReconcilers map[string]KindReconciler
}

//+kubebuilder:rbac:groups=model.skippy.io,resources=integrations,verbs=get;list;watch
//+kubebuilder:rbac:groups=model.skippy.io,resources=integrations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=model.skippy.io,resources=integrations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IntegrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Ensure r.m is initialized if it's a pointer, but it's not, so direct use is fine.
	// However, ensure reconcilers map is initialized before first Reconcile or in constructor/Setup.
	// It's better to initialize maps in the struct that creates IntegrationReconciler or in SetupWithManager.
	// For now, let's assume it's handled or add a check.

	log := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name, "controller", "Integration")

	r.m.Lock()
	defer r.m.Unlock()

	// Fetch the integration
	integration := &modelv1.Integration{}
	if err := r.Get(ctx, req.NamespacedName, integration); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Integration resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Integration resource.")
		return ctrl.Result{}, err
	}

	log.Info("Successfully fetched Integration resource", "integrationName", integration.Name)

	return r.processIntegrations(ctx, integration.Spec, log)
}

// SetupWithManager sets up the controller with the Manager.
func (r *IntegrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&modelv1.Integration{}).
		Complete(r)
}

func (r *IntegrationReconciler) processIntegrations(ctx context.Context, newIntegrations []modelv1.IntegrationSpec, log logr.Logger) (ctrl.Result, error) {
	if r.reconcilers == nil {
		r.reconcilers = map[string]*GenericReconciler{}
	}

	// This slice will hold the IntegrationSpecs that are confirmed to be active
	// (either newly added, or existing and still present).
	var activeIntegrationsThisCycle []modelv1.IntegrationSpec
	defer func() {
		r.Transformer.Registry().SetIntegrations(activeIntegrationsThisCycle)
	}()

	// Add/Update loop
	for _, newIntegrationSpec := range newIntegrations {
		gvkString := fmt.Sprintf("%s/%s/%s", newIntegrationSpec.Group, newIntegrationSpec.Version, newIntegrationSpec.Kind)

		var foundReconciler *GenericReconciler
		for key, existingRec := range r.reconcilers {
			if existingRec.Gvk.Group == newIntegrationSpec.Group && existingRec.Gvk.Version == newIntegrationSpec.Version && existingRec.Gvk.Kind == newIntegrationSpec.Kind {
				log.Info("Found existing reconciler for", "gvk", gvkString, "key", key)
				foundReconciler = existingRec
				break
			}
		}

		if foundReconciler != nil {
			if err := r.processIntegrationsUpdate(ctx, foundReconciler, newIntegrationSpec, log); err != nil {
				return ctrl.Result{}, err
			}
			activeIntegrationsThisCycle = append(activeIntegrationsThisCycle, newIntegrationSpec)
		} else {
			if err := r.processIntegrationsAdd(ctx, newIntegrationSpec, log); err != nil {
				return ctrl.Result{}, err
			}
			activeIntegrationsThisCycle = append(activeIntegrationsThisCycle, newIntegrationSpec)
		}
	}

	// Remove loop - Needs care
	// Create a list of keys to remove to avoid modifying map while iterating
	keysToRemove := []string{}
	for key, existingRec := range r.reconcilers {
		foundInNewSpec := false
		for _, newIntegrationSpec := range newIntegrations {
			if existingRec.Gvk.Group == newIntegrationSpec.Group && existingRec.Gvk.Version == newIntegrationSpec.Version && existingRec.Gvk.Kind == newIntegrationSpec.Kind {
				foundInNewSpec = true
				break
			}
		}
		if !foundInNewSpec {
			gvkString := fmt.Sprintf("%s/%s/%s", existingRec.Gvk.Group, existingRec.Gvk.Version, existingRec.Gvk.Kind)
			// The `processIntegrationsRemove` should ideally handle unregistering/stopping the dynamic controller.
			// And then the entry should be removed from r.reconcilers
			if err := r.processIntegrationsRemove(ctx, existingRec, log); err != nil {
				// If removal fails, we might want to requeue or handle the error.
				// For now, just log and continue to attempt other removals.
				log.Error(err, "Failed to process removal for reconciler", "gvk", gvkString)
			}
			keysToRemove = append(keysToRemove, key)
		}
	}

	for _, key := range keysToRemove {
		delete(r.reconcilers, key)
	}

	return ctrl.Result{}, nil
}

func (r *IntegrationReconciler) processIntegrationsAdd(ctx context.Context, integration modelv1.IntegrationSpec, log logr.Logger) error {
	// Create a descriptive name for the event recorder.
	// This name will appear as the 'source' of the events.
	var recorderName string
	if integration.Group == "" { // For core Kubernetes types
		recorderName = fmt.Sprintf("%s-%s-controller",
			strings.ToLower(integration.Version),
			strings.ToLower(integration.Kind))
	} else {
		recorderName = fmt.Sprintf("%s-%s-%s-controller",
			strings.ToLower(integration.Group),
			strings.ToLower(integration.Version),
			strings.ToLower(integration.Kind))
	}

	//Use the NewDiscoveryClient function from the transformer package
	discoveryClientFactory := func() (discovery.DiscoveryInterface, error) {
		cfg, err := ctrl.GetConfig()
		if err != nil {
			return nil, err
		}
		return transformer.NewDiscoveryClient(cfg)
	}

	reconciler := &GenericReconciler{
		Mutex:  &r.genericMutex,
		Client: r.Manager.GetClient(),
		Scheme: r.Manager.GetScheme(),
		Gvk: schema.GroupVersionKind{
			Group:   integration.Group,
			Version: integration.Version,
			Kind:    integration.Kind,
		},
		Transformer: r.Transformer,
		Recorder:    r.Manager.GetEventRecorderFor(recorderName), // Assign the recorder
		resourceClientFactory: func(dynClient dynamic.Interface) modelv1.ResourceClientInterface {
			return &ResourceClient{dynClient: dynClient}
		},
		discoveryClientFactory: discoveryClientFactory,
		KindReconcilers:        r.KindReconcilers,
	}

	setupFunc := r.setupGenericReconcilerFunc
	if setupFunc == nil {
		// This is the production path
		setupFunc = func(gr *GenericReconciler) error {
			return gr.SetupWithManager(r.Manager)
		}
	}

	controller := fmt.Sprintf("%s/%s/%s", integration.Group, integration.Version, integration.Kind)

	// Call the selected function (either the real one or the mock)
	if err := setupFunc(reconciler); err != nil {
		log.Error(err, "unable to set up controller", "controller", controller)
		return err
	}
	log.Info("Added controller", "controller", controller)
	r.reconcilers[controller] = reconciler
	return nil
}

func (r *IntegrationReconciler) processIntegrationsUpdate(ctx context.Context, reconciler *GenericReconciler, integration modelv1.IntegrationSpec, log logr.Logger) error {
	controller := fmt.Sprintf("%s/%s/%s", integration.Group, integration.Version, integration.Kind)
	log.Info("Updated controller", "controller", controller)
	return nil
}

func (r *IntegrationReconciler) processIntegrationsRemove(ctx context.Context, reconciler *GenericReconciler, log logr.Logger) error {
	// TODO: actually remove the handler
	controller := fmt.Sprintf("%s/%s/%s", reconciler.Gvk.Group, reconciler.Gvk.Version, reconciler.Gvk.Kind)
	log.Info("Removed controller", "controller", controller)
	return nil
}
