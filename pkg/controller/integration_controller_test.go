package controller

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	cfg "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
)

// --- Mock Implementations ---
// We need mocks for the manager and the transformer/registry.

// MockManager satisfies the parts of ctrl.Manager we use.
type MockManager struct {
	client client.Client
	scheme *runtime.Scheme
}

func (m *MockManager) GetClient() client.Client   { return m.client }
func (m *MockManager) GetScheme() *runtime.Scheme { return m.scheme }
func (m *MockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return record.NewFakeRecorder(10)
}

// Add other ctrl.Manager methods here if your code starts using them,
// otherwise they can be left unimplemented for this test.
func (m *MockManager) GetConfig() *rest.Config                                  { return nil }
func (m *MockManager) GetFieldIndexer() client.FieldIndexer                     { return nil }
func (m *MockManager) GetAPIReader() client.Reader                              { return nil }
func (m *MockManager) GetWebhookServer() webhook.Server                         { return nil }
func (m *MockManager) Add(runnable manager.Runnable) error                      { return nil }
func (m *MockManager) Start(ctx context.Context) error                          { return nil }
func (m *MockManager) SetFields(interface{}) error                              { return nil }
func (m *MockManager) GetLogger() logr.Logger                                   { return logr.Discard() }
func (m *MockManager) Elected() <-chan struct{}                                 { return nil }
func (m *MockManager) GetRESTMapper() meta.RESTMapper                           { return nil }
func (m *MockManager) AddHealthzCheck(name string, check healthz.Checker) error { return nil }
func (m *MockManager) AddReadyzCheck(name string, check healthz.Checker) error  { return nil }

var _ = Describe("IntegrationReconciler", func() {
	var (
		reconciler      *IntegrationReconciler
		mockManager     *MockManager
		mockTransformer *MockTransformer
		mockRegistry    *MockRegistry
		fakeK8sClient   client.Client
		ctx             context.Context
		scheme          *runtime.Scheme
		setupCalls      map[string]int
		// Define some GVKs for testing
		modelDataGVK  = schema.GroupVersionKind{Group: "model.skippy.io", Version: "v1", Kind: "ModelData"}
		deploymentGVK = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
		endpointGVK   = schema.GroupVersionKind{Group: "model.skippy.io", Version: "v1", Kind: "Endpoint"}
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(modelv1.AddToScheme(scheme)).To(Succeed()) // Add our own API types to the scheme
		Expect(corev1.AddToScheme(scheme)).To(Succeed())  // Add core types

		// Setup mocks
		mockRegistry = &MockRegistry{}
		mockTransformer = &MockTransformer{
			RegistryFunc: func() modelv1.RegistryInterface {
				return mockRegistry
			},
		}

		fakeK8sClient = fake.NewClientBuilder().WithScheme(scheme).Build()

		mockManager = &MockManager{
			client: fakeK8sClient,
			scheme: scheme,
		}
		setupCalls = make(map[string]int)
		// Instantiate the reconciler with mocks
		reconciler = &IntegrationReconciler{
			Client:      fakeK8sClient,
			Manager:     mockManager,
			Transformer: mockTransformer,
			Scheme:      scheme,
			reconcilers: make(map[string]*GenericReconciler), // Initialize the map
			setupGenericReconcilerFunc: func(gr *GenericReconciler) error {
				// This mock just records that a setup was attempted for a GVK.
				// It does NOT call the real SetupWithManager.
				key := gvkToString(gr.Gvk)
				setupCalls[key]++
				return nil // Always succeed in the mock
			},
		}
	})

	Context("Reconciliation Logic", func() {
		It("should do nothing if the Integration resource is not found", func() {
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "non-existent", Namespace: "default"}}
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should add new reconcilers when an Integration CR is created", func() {
			// ARRANGE
			// Define the specs that will be in our CRD
			integrationSpecs := []modelv1.IntegrationSpec{
				{Group: modelDataGVK.Group, Version: modelDataGVK.Version, Kind: modelDataGVK.Kind},
				{Group: deploymentGVK.Group, Version: deploymentGVK.Version, Kind: deploymentGVK.Kind},
			}
			integrationCR := &modelv1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-integration", Namespace: "default"},
				Spec:       integrationSpecs,
			}
			Expect(fakeK8sClient.Create(ctx, integrationCR)).To(Succeed())

			// Track calls to SetIntegrations
			var setIntegrationsCallCount int
			var capturedSpecs []modelv1.IntegrationSpec
			mockRegistry.SetIntegrationsFunc = func(integrations []modelv1.IntegrationSpec) {
				setIntegrationsCallCount++
				capturedSpecs = integrations
			}

			// ACT
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-integration", Namespace: "default"}}
			_, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).NotTo(HaveOccurred())

			// Check that two generic reconcilers were created and stored
			// Instead of checking the internal map directly, we verify that our
			// mock setup function was called for the correct kinds.
			Expect(setupCalls).To(HaveLen(2))
			Expect(setupCalls).To(HaveKey(gvkToString(modelDataGVK)))
			Expect(setupCalls).To(HaveKey(gvkToString(deploymentGVK)))

			// This assertion for SetIntegrations should now pass because the panic is gone
			Expect(setIntegrationsCallCount).To(Equal(1))
			Expect(capturedSpecs).To(ConsistOf(integrationSpecs))
		})

		It("should remove an obsolete reconciler when an Integration CR is updated", func() {
			// ARRANGE
			// Pre-populate the reconciler state to simulate that ModelData was previously managed
			existingModelDataReconciler := &GenericReconciler{Gvk: modelDataGVK}
			reconciler.reconcilers[gvkToString(modelDataGVK)] = existingModelDataReconciler

			// The new spec only contains the Deployment
			newIntegrationSpecs := []modelv1.IntegrationSpec{
				{Group: deploymentGVK.Group, Version: deploymentGVK.Version, Kind: deploymentGVK.Kind},
			}
			integrationCR := &modelv1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-integration", Namespace: "default"},
				Spec:       newIntegrationSpecs,
			}
			Expect(fakeK8sClient.Create(ctx, integrationCR)).To(Succeed())

			// ACT
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-integration", Namespace: "default"}}
			_, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).NotTo(HaveOccurred())

			Expect(setupCalls).To(HaveLen(1))
			Expect(setupCalls).To(HaveKey(gvkToString(deploymentGVK)))
			// Check that the reconciler map was updated correctly (ModelData removed, Deployment added)
			Expect(reconciler.reconcilers).To(HaveLen(1))
			Expect(reconciler.reconcilers).NotTo(HaveKey(gvkToString(modelDataGVK))) // Should be removed
			Expect(reconciler.reconcilers).To(HaveKey(gvkToString(deploymentGVK)))   // Should be added
		})

		It("should remove all reconcilers when the Integration CR spec is empty", func() {
			// ARRANGE
			// Pre-populate the reconciler state
			reconciler.reconcilers[gvkToString(modelDataGVK)] = &GenericReconciler{Gvk: modelDataGVK}
			reconciler.reconcilers[gvkToString(endpointGVK)] = &GenericReconciler{Gvk: endpointGVK}

			// The new spec is empty
			integrationCR := &modelv1.Integration{
				ObjectMeta: metav1.ObjectMeta{Name: "test-integration", Namespace: "default"},
				Spec:       []modelv1.IntegrationSpec{},
			}
			Expect(fakeK8sClient.Create(ctx, integrationCR)).To(Succeed())

			var capturedSpecs []modelv1.IntegrationSpec
			mockRegistry.SetIntegrationsFunc = func(integrations []modelv1.IntegrationSpec) {
				capturedSpecs = integrations
			}

			// ACT
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-integration", Namespace: "default"}}
			_, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).NotTo(HaveOccurred())
			// The reconcilers map should now be empty
			Expect(reconciler.reconcilers).To(HaveLen(0))
			// The registry should be set with an empty slice
			Expect(capturedSpecs).To(BeEmpty())
		})
	})
})

// Helper to create a consistent string key from a GVK
func gvkToString(gvk schema.GroupVersionKind) string {
	return fmt.Sprintf("%s/%s/%s", gvk.Group, gvk.Version, gvk.Kind)
}

func (m *MockManager) AddMetricsServerExtraHandler(path string, handler http.Handler) error {
	// This method is required to satisfy the ctrl.Manager interface.
	// We don't need it for these tests, so it can just return nil.
	return nil
}
func (m *MockManager) GetCache() cache.Cache {
	// Return a "typed nil" which is a valid nil value for an interface type.
	var c cache.Cache
	return c
}

func (m *MockManager) GetControllerOptions() cfg.Controller {
	// This method is required to satisfy the ctrl.Manager interface.
	// We can return an empty struct as our tests do not use this.
	return cfg.Controller{}
}

func (m *MockManager) GetHTTPClient() *http.Client {
	// This method is required to satisfy the ctrl.Manager interface.
	// We don't need it for these tests, so it can just return nil.
	return nil
}
