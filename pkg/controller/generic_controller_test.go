package controller

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
)

// Helper to create a logger for tests
func testLogger() logr.Logger {
	return log.Log.WithName("test")
}

func TestGetStringValue(t *testing.T) {
	m := map[string]interface{}{"foo": "bar"}
	if got := getStringValue(m, "foo"); got != "bar" {
		t.Errorf("getStringValue() = %v, want %v", got, "bar")
	}
	if got := getStringValue(m, "missing"); got != "" {
		t.Errorf("getStringValue() = %v, want empty string", got)
	}
}

func TestGetBoolValue(t *testing.T) {
	m := map[string]interface{}{"b1": true, "b2": "true", "b3": "false"}
	if !getBoolValue(m, "b1") {
		t.Error("getBoolValue() for true failed")
	}
	if !getBoolValue(m, "b2") {
		t.Error("getBoolValue() for 'true' failed")
	}
	if getBoolValue(m, "b3") {
		t.Error("getBoolValue() for 'false' failed")
	}
}

func TestGetStringList(t *testing.T) {
	m := map[string]interface{}{"args": []interface{}{"a", "b"}}
	got := getStringList(m, "args")
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getStringList() = %v, want %v", got, want)
	}
}

func TestGetEnvVars(t *testing.T) {
	cm := map[string]interface{}{
		"env": []interface{}{
			map[string]interface{}{"name": "FOO", "value": "bar"},
			map[string]interface{}{"name": "BAR", "value": "baz"},
		},
	}
	got := getEnvVars(cm)
	if len(got) != 2 || got[0].Name != "BAR" || got[1].Name != "FOO" {
		t.Errorf("getEnvVars() = %v", got)
	}
}

func TestGetVolumeMounts(t *testing.T) {
	cm := map[string]interface{}{
		"volumeMounts": []interface{}{
			map[string]interface{}{"name": "vol1", "mountPath": "/data", "readOnly": true},
			map[string]interface{}{"name": "vol2", "mountPath": "/cache", "readOnly": false},
		},
	}
	got := getVolumeMounts(cm)
	if len(got) != 2 || got[0].Name != "vol1" || !got[0].ReadOnly {
		t.Errorf("getVolumeMounts() = %v", got)
	}
}

func TestGetResourceList(t *testing.T) {
	rm := map[string]interface{}{
		"limits": map[string]interface{}{
			"cpu":    "100m",
			"memory": "128Mi",
		},
	}
	got := getResourceList(rm, "limits", testLogger())
	if len(got) != 2 {
		t.Errorf("getResourceList() = %v", got)
	}
}

func TestGetInt32ValueFromInterface(t *testing.T) {
	l := testLogger()
	tests := []struct {
		val    interface{}
		expect int32
	}{
		{int(5), 5},
		{int32(6), 6},
		{int64(7), 7},
		{float64(8), 8},
		{"9", 9},
		{"1Gi", 1073741824},
		{nil, 0},
	}
	for _, tt := range tests {
		got := getInt32ValueFromInterface(tt.val, l)
		if got != tt.expect {
			t.Errorf("getInt32ValueFromInterface(%v) = %v, want %v", tt.val, got, tt.expect)
		}
	}
}

func TestGetFloat64ValueFromInterface(t *testing.T) {
	l := testLogger()
	tests := []struct {
		val    interface{}
		expect float64
	}{
		{int(5), 5.0},
		{int32(6), 6.0},
		{int64(7), 7.0},
		{float64(8.5), 8.5},
		{"9.5", 9.5},
		{"1Gi", float64(1073741824)},
		{nil, 0.0},
	}
	for _, tt := range tests {
		got := getFloat64ValueFromInterface(tt.val, l)
		if got != tt.expect {
			t.Errorf("getFloat64ValueFromInterface(%v) = %v, want %v", tt.val, got, tt.expect)
		}
	}
}

func TestGetNestedMap(t *testing.T) {
	m := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{"foo": "bar"},
			},
		},
	}
	got, err := getNestedMap(m, "spec", "template", "spec")
	if err != nil || got["foo"] != "bar" {
		t.Errorf("getNestedMap() = %v, err = %v", got, err)
	}
}

func TestCleanPodSpec(t *testing.T) {
	ps := &corev1.PodSpec{
		Containers: []corev1.Container{
			{Name: "c1", Image: "nginx"},
		},
		Volumes:      []corev1.Volume{{Name: "v1"}},
		NodeSelector: map[string]string{"foo": "bar"},
	}
	cleaned := cleanPodSpec(ps)
	if len(cleaned.Containers) != 1 || cleaned.Volumes[0].Name != "v1" {
		t.Errorf("cleanPodSpec() = %v", cleaned)
	}
}

func TestGetConfigMapDiff(t *testing.T) {
	r := &GenericReconciler{}
	obj1 := &unstructured.Unstructured{}
	obj1.Object = map[string]interface{}{"data": map[string]interface{}{"foo": "bar"}}
	obj2 := &unstructured.Unstructured{}
	obj2.Object = map[string]interface{}{"data": map[string]interface{}{"foo": "baz"}}
	diff, _ := r.configMapDiff(obj1, obj2, testLogger())
	if !diff {
		t.Error("configMapDiff should detect difference")
	}
}

func TestBuildConditions(t *testing.T) {
	r := &GenericReconciler{}
	obj := &unstructured.Unstructured{}
	obj.SetGeneration(1)
	obj.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":               ReadyConditionType,
				"status":             "False",
				"lastTransitionTime": time.Now().Format(time.RFC3339Nano),
				"reason":             "OldReason",
				"message":            "Old message",
				"observedGeneration": int64(0),
			},
		},
	}
	conds, err := r.buildConditions(context.Background(), obj, false, nil)
	if err != nil || len(conds) == 0 {
		t.Errorf("buildConditions() error = %v, conds = %v", err, conds)
	}
}

// MockResourceClient allows us to control the behavior of the dynamic resource client.
type MockResourceClient struct {
	GetFunc    func(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)
	CreateFunc func(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)
	UpdateFunc func(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)
}

func (m *MockResourceClient) Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	if m.GetFunc != nil {
		return m.GetFunc(ctx, gvk, namespace, name)
	}
	return nil, errors.NewNotFound(gvk.GroupVersion().WithResource(gvk.Kind).GroupResource(), name)
}
func (m *MockResourceClient) Create(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, gvk, namespace, obj)
	}
	return obj, nil
}
func (m *MockResourceClient) Update(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, gvk, namespace, obj)
	}
	return obj, nil
}

var _ = Describe("GenericReconciler", func() {
	var (
		reconciler      *GenericReconciler
		mockTransformer *MockTransformer
		mockRegistry    *MockRegistry
		mockResClient   *MockResourceClient
		fakeK8sClient   client.Client
		recorder        *record.FakeRecorder
		ctx             context.Context
		scheme          *runtime.Scheme

		targetGVK = schema.GroupVersionKind{Group: "testing.karo.pkg.com", Version: "v1", Kind: "TestResource"}
		depGVK    = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		scheme.AddKnownTypeWithName(targetGVK, &unstructured.Unstructured{})
		// Setup mocks
		mockTransformer = &MockTransformer{}
		mockRegistry = &MockRegistry{}
		mockResClient = &MockResourceClient{}

		// Default mock behavior
		mockTransformer.RegistryFunc = func() modelv1.RegistryInterface {
			return mockRegistry
		}
		mockRegistry.HasIntegrationFunc = func(gvk schema.GroupVersionKind) bool {
			return true // Assume integration exists for most tests
		}
		objectWithStatus := &unstructured.Unstructured{
			Object: make(map[string]interface{}),
		}
		objectWithStatus.SetGroupVersionKind(targetGVK)
		// Explicitly set apiVersion and kind in the map
		objectWithStatus.Object["apiVersion"] = targetGVK.GroupVersion().String()
		objectWithStatus.Object["kind"] = targetGVK.Kind

		// Setup fake Kubernetes client
		fakeK8sClient = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(objectWithStatus).Build()
		recorder = record.NewFakeRecorder(10)

		// Instantiate the reconciler with mocks
		reconciler = &GenericReconciler{
			Mutex:       &sync.Mutex{},
			Client:      fakeK8sClient,
			Scheme:      scheme,
			Transformer: mockTransformer,
			Gvk:         targetGVK,
			Recorder:    recorder,
			resourceClientFactory: func(dynamic.Interface) modelv1.ResourceClientInterface {
				return mockResClient
			},
			discoveryClientFactory: func() (discovery.DiscoveryInterface, error) {
				return nil, nil // Not testing this part, can be enhanced with a discovery mock
			},
		}
	})

	Context("Reconcile Logic", func() {
		It("should do nothing if no integration exists for the GVK", func() {
			// ARRANGE: Configure mock to report no integration
			mockRegistry.HasIntegrationFunc = func(gvk schema.GroupVersionKind) bool {
				return false
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-resource", Namespace: "default"}}

			// ACT
			result, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should create a dependent resource if it does not exist", func() {

			mockRegistry.HasIntegrationFunc = func(gvk schema.GroupVersionKind) bool {
				return true
			}

			// ARRANGE
			target := newTestResource("test-resource", "default", targetGVK)
			dependent := newTestDependent("test-cm", "default", depGVK)
			Expect(fakeK8sClient.Create(ctx, target)).To(Succeed())

			// Configure mocks
			mockTransformer.RunFunc = func(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, rClient client.Client, req ctrl.Request, obj *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
				return []*unstructured.Unstructured{dependent}, nil
			}
			mockResClient.GetFunc = func(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
				return nil, errors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: "configmaps"}, name)
			}
			createCalled := false
			mockResClient.CreateFunc = func(ctx context.Context, gvk schema.GroupVersionKind, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
				createCalled = true
				Expect(obj.GetKind()).To(Equal(depGVK.Kind))
				Expect(obj.GetName()).To(Equal(dependent.GetName()))
				return obj, nil
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-resource", Namespace: "default"}}

			// ACT
			_, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).NotTo(HaveOccurred())
			Expect(createCalled).To(BeTrue())

			// Check that status was updated
			updatedTarget := &unstructured.Unstructured{}
			updatedTarget.SetGroupVersionKind(targetGVK)
			Expect(fakeK8sClient.Get(ctx, req.NamespacedName, updatedTarget)).To(Succeed())
			conditions, _, _ := unstructured.NestedSlice(updatedTarget.Object, "status", "conditions")
			Expect(conditions).To(HaveLen(1))
			readyCondition := conditions[0].(map[string]interface{})
			Expect(readyCondition["type"]).To(Equal(ReadyConditionType))
			Expect(readyCondition["status"]).To(Equal(string(metav1.ConditionTrue)))
		})

		It("should update a dependent resource if it has changed", func() {
			// ARRANGE
			target := newTestResource("test-resource", "default", targetGVK)
			existingDependent := newTestDependent("test-cm", "default", depGVK)
			unstructured.SetNestedField(existingDependent.Object, map[string]interface{}{"old-key": "old-value"}, "data")

			desiredDependent := newTestDependent("test-cm", "default", depGVK)
			unstructured.SetNestedField(desiredDependent.Object, map[string]interface{}{"new-key": "new-value"}, "data")

			Expect(fakeK8sClient.Create(ctx, target)).To(Succeed())

			// Configure mocks
			mockTransformer.RunFunc = func(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, rClient client.Client, req ctrl.Request, obj *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
				return []*unstructured.Unstructured{desiredDependent}, nil
			}
			mockResClient.GetFunc = func(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
				return existingDependent, nil
			}
			updateCalled := false
			mockResClient.UpdateFunc = func(ctx context.Context, gvk schema.GroupVersionKind, namespace string, objInUpdate *unstructured.Unstructured) (*unstructured.Unstructured, error) {
				updateCalled = true
				data, _, _ := unstructured.NestedMap(objInUpdate.Object, "data")
				Expect(data).To(HaveKey("new-key"))
				return objInUpdate, nil
			}
			// This test uses the default diff function. We mock that it returns true.
			// To test the real diff function, you would pass in a real Service.
			reconciler.getResourceReconciler = func(kind string) (*ResourceReconciler, error) {
				// For this test, we return a reconciler whose diffFunc always returns true.
				return &ResourceReconciler{diffFunc: func(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
					return true, nil // Force a diff to trigger the update path
				}}, nil
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-resource", Namespace: "default"}}

			// ACT
			_, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).NotTo(HaveOccurred())
			Expect(updateCalled).To(BeTrue())
		})

		It("should return an error if the transformer fails", func() {
			// ARRANGE
			target := newTestResource("test-resource", "default", targetGVK)
			Expect(fakeK8sClient.Create(ctx, target)).To(Succeed())
			transformerError := fmt.Errorf("transformer failed")

			mockTransformer.RunFunc = func(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, rClient client.Client, req ctrl.Request, obj *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
				return nil, transformerError
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-resource", Namespace: "default"}}

			// ACT
			_, err := reconciler.Reconcile(ctx, req)

			// ASSERT
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(transformerError.Error()))

			// Check that status was updated with failure
			updatedTarget := &unstructured.Unstructured{}
			updatedTarget.SetGroupVersionKind(targetGVK)
			Expect(fakeK8sClient.Get(ctx, req.NamespacedName, updatedTarget)).To(Succeed())
			conditions, _, _ := unstructured.NestedSlice(updatedTarget.Object, "status", "conditions")
			Expect(conditions).To(HaveLen(1))
			readyCondition := conditions[0].(map[string]interface{})
			Expect(readyCondition["type"]).To(Equal(ReadyConditionType))
			Expect(readyCondition["status"]).To(Equal(string(metav1.ConditionFalse)))
			Expect(readyCondition["reason"]).To(Equal(ReconciliationFailedReason))
		})
	})

	Context("defaultGetResourceReconciler method", func() {
		It("should return a valid reconciler for supported kinds", func() {
			supportedKinds := []string{"Deployment", "Service", "Secret", "ConfigMap", "Job", "HorizontalPodAutoscaler", "PodMonitoring"}
			for _, kind := range supportedKinds {
				// Use the 'reconciler' instance from BeforeEach
				rr, err := reconciler.defaultGetResourceReconciler(kind)
				Expect(err).NotTo(HaveOccurred(), "for kind "+kind)
				Expect(rr).NotTo(BeNil(), "for kind "+kind)
				Expect(rr.diffFunc).NotTo(BeNil(), "for kind "+kind)
			}
		})

		It("should return an error for an unsupported kind", func() {
			// Use the 'reconciler' instance from BeforeEach
			_, err := reconciler.defaultGetResourceReconciler("UnsupportedKind")
			Expect(err).To(HaveOccurred())
		})
	})
})

// --- Helper Functions ---

func newTestResource(name, namespace string, gvk schema.GroupVersionKind) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetGeneration(1)
	obj.SetUID(types.UID("test-uid-" + name))

	// THE FIX: Also set these fields in the map for the Create() call.
	if obj.Object == nil {
		obj.Object = make(map[string]interface{})
	}
	obj.Object["apiVersion"] = gvk.GroupVersion().String()
	obj.Object["kind"] = gvk.Kind

	return obj
}

func newTestDependent(name, namespace string, gvk schema.GroupVersionKind) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)

	// THE FIX: Also set these fields in the map.
	if obj.Object == nil {
		obj.Object = make(map[string]interface{})
	}
	obj.Object["apiVersion"] = gvk.GroupVersion().String()
	obj.Object["kind"] = gvk.Kind

	return obj
}
