// Package transformer defines components for transforming Kubernetes resources.
// This file contains interface definitions for improved testability.
package v1

import (
	"context"

	// Kubernetes types
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	// Controller-runtime types
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceClientInterface defines the interface for interacting with Kubernetes resources.
type ResourceClientInterface interface {
	Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	Create(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)
	Update(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)
}

// RegistryInterface defines the methods required from the IntegrationRegistry
// by other components (like GenericReconciler and IntegrationReconciler).
type RegistryInterface interface {
	// HasIntegration checks if an integration exists for the given GVK. (Used by GenericReconciler)
	HasIntegration(gvk schema.GroupVersionKind) bool

	// LockIntegrations acquires a lock for modifying the integration list. (Used by IntegrationReconciler)
	LockIntegrations() func()

	// SetIntegrations updates the list of active integrations. (Used by IntegrationReconciler)
	SetIntegrations(integrations []IntegrationSpec)

	ListIntegrations() []schema.GroupVersionKind

	// Add other IntegrationRegistry methods here IF they are directly
	// called by GenericReconciler or IntegrationReconciler via the transformer.Registry() method.
	ResolveContext(ctx context.Context, resource *unstructured.Unstructured, output map[string]any) error
	GetCopyPaths(k schema.GroupVersionKind) []string
	GetTemplatePaths(k schema.GroupVersionKind) []string
	GetReferencePaths(k schema.GroupVersionKind) (map[schema.GroupVersionKind]string, map[schema.GroupVersionKind]string)
	GetReferenceRules(gvk schema.GroupVersionKind) []IntegrationApiReferenceSpec
}

// TransformerInterface defines the methods required from the Transformer
// by other components (like GenericReconciler and IntegrationReconciler).
type TransformerInterface interface {
	// Run executes the transformation logic for a given primary resource.
	// It accepts discovery.DiscoveryInterface for better mockability in tests.
	Run(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, mapper meta.RESTMapper, rClient client.Client, req ctrl.Request, obj *unstructured.Unstructured) ([]*unstructured.Unstructured, error)

	// Registry returns the registry component satisfying the RegistryInterface.
	Registry() RegistryInterface
}

// --- Compile-time checks to ensure real types satisfy the interfaces ---
// Place these checks near the concrete type definitions (e.g., in integrationRegistry.go and transform.go)
// or keep them here for visibility.

// Ensure IntegrationRegistry satisfies RegistryInterface.
// Add this line (or uncomment if placed here) in integrationRegistry.go or similar.
// var _ RegistryInterface = (*IntegrationRegistry)(nil)

// Ensure Transformer satisfies TransformerInterface.
// Add this line (or uncomment if placed here) in transform.go or similar.
// This check might fail initially if the real Transformer.Run signature
// still uses *discovery.DiscoveryClient instead of discovery.DiscoveryInterface.
// var _ TransformerInterface = (*Transformer)(nil)
