package transformer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	template "github.com/google/safetext/yamltemplate"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"

	// For FindDefaultCredentials
	"golang.org/x/oauth2/google"
	"k8s.io/client-go/discovery"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"github.com/Masterminds/sprig/v3"
	"k8s.io/client-go/rest"
)

var _ modelv1.RegistryInterface = (*IntegrationRegistry)(nil)

// IntegrationRegistry struct
type IntegrationRegistry struct {
	m            sync.RWMutex
	integrations []modelv1.IntegrationSpec
	httpClient   *http.Client
}

// NewIntegrationRegistry returns a new model.
func NewIntegrationRegistry() *IntegrationRegistry {
	// Create the real client here for production use.
	// This call will only happen once when the registry is created.
	scopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
	}
	client, err := google.DefaultClient(context.Background(), scopes...)
	if err != nil {
		// In a real application, you might want to panic or have a fallback
		// if the default client can't be created.
		log.Log.Error(err, "Failed to create default google client for IntegrationRegistry")
	}

	return &IntegrationRegistry{
		integrations: []modelv1.IntegrationSpec{},
		httpClient:   client, // Store the client
	}
}

// SetIntegrations allows to set the integrations
func (m *IntegrationRegistry) SetIntegrations(integrations []modelv1.IntegrationSpec) {
	m.m.Lock()
	defer m.m.Unlock()
	m.integrations = integrations
}

func (m *IntegrationRegistry) LockIntegrations() func() {
	m.m.Lock()
	return m.m.Unlock
}

func (m *IntegrationRegistry) SetIntegrationsAndUnlock(integrations []modelv1.IntegrationSpec) func() {
	m.m.Lock()
	m.integrations = integrations
	return m.m.Unlock
}

// HasIntegration return true if the current model has the named integration.
func (m *IntegrationRegistry) HasIntegration(k schema.GroupVersionKind) bool {
	m.m.RLock()
	defer m.m.RUnlock()

	return slices.ContainsFunc(m.integrations, func(ii modelv1.IntegrationSpec) bool {
		return ii.Group == k.Group && ii.Version == k.Version && ii.Kind == k.Kind
	})
}

// ListIntegrations lists all integrations.
func (m *IntegrationRegistry) ListIntegrations() []schema.GroupVersionKind {
	m.m.RLock()
	defer m.m.RUnlock()

	var result []schema.GroupVersionKind
	for _, i := range m.integrations {
		gvk := schema.GroupVersionKind{
			Group:   i.Group,
			Version: i.Version,
			Kind:    i.Kind,
		}
		result = append(result, gvk)
	}
	return result
}

// GetReferencePaths returns the reference paths for the specified kind. The first map contains the
// name paths and the second contains the namespace paths.
func (m *IntegrationRegistry) GetReferencePaths(k schema.GroupVersionKind) (map[schema.GroupVersionKind]string, map[schema.GroupVersionKind]string) {
	m.m.RLock()
	defer m.m.RUnlock()

	names := map[schema.GroupVersionKind]string{}
	namespaces := map[schema.GroupVersionKind]string{}
	i, ok := m.findIntegration(k)
	if !ok {
		return names, namespaces
	}
	for _, r := range i.References {
		gvk := schema.GroupVersionKind{
			Group:   r.Group,
			Version: r.Version,
			Kind:    r.Kind,
		}
		names[gvk] = r.Paths.Name
		namespaces[gvk] = r.Paths.Namespace
	}
	return names, namespaces
}

// GetReferenceRules returns the complete reference rule objects for a given GVK.
// This allows the transformer to access the `propagateTemplates` flag.
func (m *IntegrationRegistry) GetReferenceRules(gvk schema.GroupVersionKind) []modelv1.IntegrationApiReferenceSpec {
	m.m.RLock()
	defer m.m.RUnlock()

	// Reuse your existing helper function to find the correct integration spec.
	integrationSpec, ok := m.findIntegration(gvk)
	if !ok {
		// If no integration is found for this GVK, there are no rules to return.
		return nil
	}

	// Return the complete slice of reference rules from the found integration.
	return integrationSpec.References
}

// ResolveContext returns the context for the specified resource.
func (m *IntegrationRegistry) ResolveContext(ctx context.Context, resource *unstructured.Unstructured, output map[string]any) error {
	m.m.RLock()
	defer m.m.RUnlock()

	log := log.FromContext(ctx).WithValues("integration", "Integration")

	log.Info("Constructing context...")

	i, ok := m.findIntegration(resource.GroupVersionKind())
	if !ok {
		return fmt.Errorf("missing integration for %q", resource.GroupVersionKind().String())
	}

	//scopes := []string{
	//	"https://www.googleapis.com/auth/cloud-platform",
	//}
	//client, err := google.DefaultClient(context.Background(), scopes...)
	client := m.httpClient
	if client == nil {
		return fmt.Errorf("http client is not initialized in IntegrationRegistry")
	}

	for _, ctxConfig := range i.Context { // Changed ctx to ctxConfig to avoid confusion with the context
		method := ctxConfig.Request.Method
		path := ctxConfig.Request.Path
		if method != "GET" {
			return fmt.Errorf("invalid request. only GET supported")
		}
		temp, err := template.New(path).Funcs(sprig.FuncMap()).Funcs(template.FuncMap{
			"urlEncodeModelName": urlEncodeModelName,
		}).Parse(path)
		if err != nil {
			return err
		}
		builder := strings.Builder{}
		if err := temp.Execute(&builder, output); err != nil {
			return err
		}

		requestURL := builder.String() // Store the URL

		res, err := client.Get(requestURL)
		if err != nil {
			return err
		}
		defer res.Body.Close() // Ensure the body is closed

		if res.StatusCode != http.StatusOK {
			// Read the response body to get more information about the error
			errorBody, err := io.ReadAll(res.Body)
			if err != nil {
				return fmt.Errorf("invalid return: %d. And could not read body", res.StatusCode)
			}
			return fmt.Errorf("invalid return: %d. Body: %s", res.StatusCode, string(errorBody))
		}

		buffer, err := io.ReadAll(res.Body)
		if err != nil {
			return err
		}
		var body any
		if err := json.Unmarshal(buffer, &body); err != nil {
			return err
		}
		output[ctxConfig.Name] = body
	}
	return nil
}

func urlEncodeModelName(input string) (string, error) {
	// A simple check for an empty string is still good practice.
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("model name cannot be empty")
	}

	// The strict check for a '/' is removed.
	// We now simply encode whatever valid string we are given.
	encoded := url.QueryEscape(input)
	encoded = replacePlusWithPercent20(encoded)
	return encoded, nil
}

func replacePlusWithPercent20(input string) string {
	return strings.ReplaceAll(input, "+", "%20")
}

// GetCopyPaths returns the copy paths for the specified kind.
func (m *IntegrationRegistry) GetCopyPaths(k schema.GroupVersionKind) []string {
	m.m.RLock()
	defer m.m.RUnlock()

	return m.getPaths(k, "copy")
}

// GetTemplatePaths returns the template paths for the specified kind.
func (m *IntegrationRegistry) GetTemplatePaths(k schema.GroupVersionKind) []string {
	m.m.RLock()
	defer m.m.RUnlock()

	return m.getPaths(k, "template")
}

func (m *IntegrationRegistry) getPaths(k schema.GroupVersionKind, operation string) []string {
	paths := []string{}
	i, ok := m.findIntegration(k)
	if !ok {
		return paths
	}
	for _, template := range i.Templates {
		if template.Operation == operation {
			paths = append(paths, template.Path)
		}
	}
	return paths
}

func (m *IntegrationRegistry) findIntegration(k schema.GroupVersionKind) (modelv1.IntegrationSpec, bool) {
	for _, ii := range m.integrations {
		if ii.Group == k.Group && ii.Version == k.Version && ii.Kind == k.Kind {
			return ii, true
		}
	}
	return modelv1.IntegrationSpec{}, false
}

// DiscoveryClient implements DiscoveryClientInterface
type DiscoveryClient struct {
	discovery.DiscoveryInterface
}

func NewDiscoveryClient(cfg *rest.Config) (*DiscoveryClient, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &DiscoveryClient{DiscoveryInterface: discoveryClient}, nil
}
