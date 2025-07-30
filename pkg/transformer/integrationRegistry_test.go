package transformer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// MockRoundTripper is a mock implementation of http.RoundTripper for testing.
type MockRoundTripper struct {
	// Response is the HTTP response to return.
	Response *http.Response
	// Err is the error to return.
	Err error
	// RoundTripFunc allows defining custom round-trip logic for complex tests.
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

// RoundTrip implements the http.RoundTripper interface.
func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.RoundTripFunc != nil {
		return m.RoundTripFunc(req)
	}
	return m.Response, m.Err
}

func newTestRegistry() *IntegrationRegistry {
	reg := NewIntegrationRegistry()
	reg.SetIntegrations([]modelv1.IntegrationSpec{
		// Test case for a Deployment that references a Service
		{
			// Correctly use the top-level fields for GVK
			Group:   "apps",
			Version: "v1",
			Kind:    "Deployment",

			// Use the real type: IntegrationApiReferenceSpec
			References: []modelv1.IntegrationApiReferenceSpec{
				{
					// Use the real fields
					Group:   "", // Group is empty for core types like Service
					Version: "v1",
					Kind:    "Service",
					// Use the real type: IntegrationApiReferencePathSpec
					Paths: modelv1.IntegrationApiReferencePathSpec{Name: "spec.serviceName", Namespace: "metadata.namespace"},
				},
			},

			// Use the real type: IntegrationApiTemplatesSpec
			Templates: []modelv1.IntegrationApiTemplatesSpec{
				{Operation: "copy", Path: "path/to/copy"},
				{Operation: "template", Path: "path/to/template"},
			},
		},
		// Test case for a ServiceMonitor that has a context lookup
		{
			Group:   "monitoring.coreos.com",
			Version: "v1",
			Kind:    "ServiceMonitor",

			// Use the real type: IntegrationApiContextSpec
			Context: []modelv1.IntegrationApiContextSpec{
				{
					Name: "serviceInfo",
					// Use the real type: IntegrationApiContextRequestSpec
					Request: modelv1.IntegrationApiContextRequestSpec{
						Method: "GET",
						Path:   "https://example.com/services/{{ .resource.spec.serviceName }}",
					},
				},
			},
		},
	})
	return reg
}

func TestIntegrationRegistry_StateManagement(t *testing.T) {
	t.Run("Set and Has Integration", func(t *testing.T) {
		reg := NewIntegrationRegistry()
		gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

		// Initially, it should not have the integration
		if reg.HasIntegration(gvk) {
			t.Errorf("HasIntegration() got = true, want false for empty registry")
		}

		// Set an integration
		reg.SetIntegrations([]modelv1.IntegrationSpec{
			{
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
				// Other fields like Templates, References, etc., are not needed for this specific test
			},
		})

		// Now, it should have the integration
		if !reg.HasIntegration(gvk) {
			t.Errorf("HasIntegration() got = false, want true after setting integration")
		}
	})

	t.Run("ListIntegrations", func(t *testing.T) {
		reg := newTestRegistry()
		expectedGVKs := []schema.GroupVersionKind{
			{Group: "apps", Version: "v1", Kind: "Deployment"},
			{Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitor"},
		}

		listedGVKs := reg.ListIntegrations()

		if len(listedGVKs) != len(expectedGVKs) {
			t.Fatalf("ListIntegrations() returned %d items, want %d", len(listedGVKs), len(expectedGVKs))
		}
		// Note: This check assumes the order is preserved. For a more robust check,
		// you might sort both slices or compare them as sets.
		if !reflect.DeepEqual(listedGVKs, expectedGVKs) {
			t.Errorf("ListIntegrations() got = %v, want %v", listedGVKs, expectedGVKs)
		}
	})
}

func TestIntegrationRegistry_GetPaths(t *testing.T) {
	reg := newTestRegistry()
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}

	t.Run("GetCopyPaths", func(t *testing.T) {
		expected := []string{"path/to/copy"}
		got := reg.GetCopyPaths(gvk)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("GetCopyPaths() = %v, want %v", got, expected)
		}
	})

	t.Run("GetTemplatePaths", func(t *testing.T) {
		expected := []string{"path/to/template"}
		got := reg.GetTemplatePaths(gvk)
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("GetTemplatePaths() = %v, want %v", got, expected)
		}
	})

	t.Run("GetReferencePaths", func(t *testing.T) {
		refGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
		expectedNames := map[schema.GroupVersionKind]string{refGVK: "spec.serviceName"}
		expectedNamespaces := map[schema.GroupVersionKind]string{refGVK: "metadata.namespace"}

		gotNames, gotNamespaces := reg.GetReferencePaths(gvk)

		if !reflect.DeepEqual(gotNames, expectedNames) {
			t.Errorf("GetReferencePaths() names got = %v, want %v", gotNames, expectedNames)
		}
		if !reflect.DeepEqual(gotNamespaces, expectedNamespaces) {
			t.Errorf("GetReferencePaths() namespaces got = %v, want %v", gotNamespaces, expectedNamespaces)
		}
	})
}

func TestIntegrationRegistry_ResolveContext(t *testing.T) {
	// 1. Setup the mock HTTP client
	mockTripper := &MockRoundTripper{}
	mockHTTPClient := &http.Client{
		Transport: mockTripper,
	}

	// 2. Create a test registry and INJECT the mock client.
	reg := newTestRegistry()
	reg.httpClient = mockHTTPClient // <-- THIS IS THE NEW, SIMPLER WAY

	ctx := context.Background()

	t.Run("successful context resolution", func(t *testing.T) {
		// ARRANGE
		// Configure the mock response
		jsonResponse := `{"key":"value"}`
		mockTripper.Response = &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(jsonResponse)),
		}
		mockTripper.Err = nil
		// Define how the mock round tripper should behave for specific URLs
		mockTripper.RoundTripFunc = func(req *http.Request) (*http.Response, error) {
			expectedURL := "https://example.com/services/my-test-service"
			if req.URL.String() != expectedURL {
				return nil, fmt.Errorf("unexpected request URL: got %s, want %s", req.URL.String(), expectedURL)
			}
			return mockTripper.Response, mockTripper.Err
		}

		// Define the input resource and output map
		resource := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "monitoring.coreos.com/v1",
				"kind":       "ServiceMonitor",
			},
		}
		// The `output` map starts with data that can be used to render the template URL
		output := map[string]any{
			"resource": map[string]interface{}{
				"spec": map[string]interface{}{
					"serviceName": "my-test-service",
				},
			},
		}

		// ACT
		err := reg.ResolveContext(ctx, resource, output)

		// ASSERT
		if err != nil {
			t.Fatalf("ResolveContext() returned an unexpected error: %v", err)
		}

		// Check if the output map was populated correctly
		serviceInfo, ok := output["serviceInfo"]
		if !ok {
			t.Fatal("output map missing expected key 'serviceInfo'")
		}

		serviceInfoMap, ok := serviceInfo.(map[string]interface{})
		if !ok {
			t.Fatalf("serviceInfo is not a map, got %T", serviceInfo)
		}

		if val, _ := serviceInfoMap["key"].(string); val != "value" {
			t.Errorf("Expected key 'value' in resolved context, got '%s'", val)
		}
	})

	t.Run("http request fails", func(t *testing.T) {
		// ARRANGE
		// Configure the mock to return an error
		expectedErr := fmt.Errorf("network error")
		mockTripper.RoundTripFunc = func(req *http.Request) (*http.Response, error) {
			return nil, expectedErr
		}
		resource := &unstructured.Unstructured{Object: map[string]interface{}{"kind": "ServiceMonitor", "apiVersion": "monitoring.coreos.com/v1"}}
		output := map[string]any{"resource": map[string]interface{}{"spec": map[string]interface{}{"serviceName": "any"}}}

		// ACT
		err := reg.ResolveContext(ctx, resource, output)

		// ASSERT
		if err == nil {
			t.Error("ResolveContext() expected an error but got nil")
		} else if !strings.Contains(err.Error(), expectedErr.Error()) {
			t.Errorf("ResolveContext() error got = %v, want to contain %v", err, expectedErr)
		}
	})

	// This is a bit of a trick to allow mocking google.DefaultClient
	// which is a global variable in its package.
}
