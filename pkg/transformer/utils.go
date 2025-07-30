package transformer

import (
	"context"
	"strings"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"github.com/Masterminds/sprig/v3"
	template "github.com/google/safetext/yamltemplate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// --- Mock Implementations (can be shared from other test files) ---
var allTemplateFuncs = func() template.FuncMap {
	f := sprig.FuncMap()

	// Add our custom functions
	f["encodeBase64"] = encodeBase64
	f["dirnameFromFlag"] = dirnameFromFlag
	f["getGcsBucketFromURI"] = getGcsBucketFromURI
	f["getGcsPathFromURI"] = getGcsPathFromURI
	f["minPerformanceAccelerator"] = minPerformanceAccelerator
	f["extractStringAfterSlash"] = extractStringAfterSlash
	f["extractValueAfterEquals"] = extractValueAfterEquals

	// Sprig also provides these, but we can define them to be explicit.
	f["lower"] = strings.ToLower
	f["hasPrefix"] = strings.HasPrefix
	f["joinInterfaceSlice"] = joinInterfaceSlice

	f["resolveModelData"] = resolveModelData // This is a custom function that resolves model paths based on the mock registry.
	f["findResource"] = findResource
	return f
}()

type mockRegistry struct {
	refPaths      map[schema.GroupVersionKind][]modelv1.IntegrationApiReferenceSpec
	integrations  []schema.GroupVersionKind
	templatePaths map[schema.GroupVersionKind][]string // To hold template paths for tests
	copyPaths     map[schema.GroupVersionKind][]string // To hold copy paths for tests
}

// This is the implementation of the new method for the mock.
// It simply returns the full slice of rule objects.
func (m *mockRegistry) GetReferenceRules(gvk schema.GroupVersionKind) []modelv1.IntegrationApiReferenceSpec {
	// This simply returns the full slice of rule objects that you've configured
	// in the mock's `refPaths` field for any given test.
	return m.refPaths[gvk]
}

// HasIntegration now correctly iterates over the slice.
func (m *mockRegistry) HasIntegration(gvk schema.GroupVersionKind) bool {
	for _, supportedGVK := range m.integrations {
		if supportedGVK == gvk {
			return true
		}
	}
	return false
}

// GetTemplatePaths now returns configured paths, not nil.
func (m *mockRegistry) GetTemplatePaths(gvk schema.GroupVersionKind) []string {
	return m.templatePaths[gvk]
}

// GetCopyPaths now returns configured paths, not nil.
func (m *mockRegistry) GetCopyPaths(gvk schema.GroupVersionKind) []string {
	return m.copyPaths[gvk]
}

// GetReferencePaths is the mocked method. It returns the paths we've configured for a given GVK.
func (m *mockRegistry) GetReferencePaths(gvk schema.GroupVersionKind) (map[schema.GroupVersionKind]string, map[schema.GroupVersionKind]string) {
	names := map[schema.GroupVersionKind]string{}
	namespaces := map[schema.GroupVersionKind]string{}

	if refs, ok := m.refPaths[gvk]; ok {
		// 'refs' is now the slice []modelv1.IntegrationApiReferenceSpec, so we iterate over it
		for _, r := range refs {
			refGVK := schema.GroupVersionKind{Group: r.Group, Version: r.Version, Kind: r.Kind}
			names[refGVK] = r.Paths.Name
			namespaces[refGVK] = r.Paths.Namespace
		}
	}
	return names, namespaces
}

func newTestObject(group, version, kind, name string) *unstructured.Unstructured {
	apiVersion := group
	if apiVersion != "" {
		apiVersion += "/"
	}
	apiVersion += version

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": apiVersion, // This will be "v1" for services, "apps/v1" for deployments
			"kind":       kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
			"spec": make(map[string]interface{}),
		},
	}
}

// addReference adds a nested field to an object to simulate a reference.
func addReference(obj *unstructured.Unstructured, refName string, path string) {
	unstructured.SetNestedField(obj.Object, refName, strings.Split(path, ".")...)
}

// Add dummy methods to satisfy the rest of the RegistryInterface.
func (m *mockRegistry) SetIntegrations(integrations []modelv1.IntegrationSpec) {}
func (m *mockRegistry) LockIntegrations() func()                               { return func() {} }
func (m *mockRegistry) ListIntegrations() []schema.GroupVersionKind            { return nil }
func (m *mockRegistry) ResolveContext(ctx context.Context, resource *unstructured.Unstructured, output map[string]any) error {
	return nil
}
