package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
)

func getNestedMap(data map[string]interface{}, keys ...string) (map[string]interface{}, error) {
	currentMap := data
	for i, key := range keys {
		if currentMap == nil {
			return nil, fmt.Errorf("map is nil at key %s (path: %s)", key, strings.Join(keys[:i+1], "."))
		}
		value, ok := currentMap[key]
		if !ok {
			return nil, fmt.Errorf("key '%s' not found (path: %s)", key, strings.Join(keys[:i+1], "."))
		}
		if nextMap, ok := value.(map[string]interface{}); ok {
			currentMap = nextMap
		} else {
			return nil, fmt.Errorf("value for key '%s' is not a map (path: %s, type: %T)", key, strings.Join(keys[:i+1], "."), value)
		}
	}
	return currentMap, nil
}

func getStringValue(data map[string]interface{}, key string) string {
	if data == nil {
		return ""
	}
	if value, ok := data[key]; ok {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return ""
}

func getStringList(data map[string]interface{}, key string) []string {
	var list []string
	if data == nil {
		return list
	}
	if value, ok := data[key]; ok {
		if array, ok := value.([]interface{}); ok {
			for _, v := range array {
				if s, ok := v.(string); ok {
					normalizedString := strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
					list = append(list, normalizedString)
				}
			}
		}
	}
	return list
}

func getInt32Value(data map[string]interface{}, key string, log logr.Logger) int32 {
	if data == nil {
		return 0
	}
	containerPortValue := data[key]
	if intPort, ok := containerPortValue.(int); ok {
		return int32(intPort)
	} else if int64Port, ok := containerPortValue.(int64); ok {
		return int32(int64Port)
	} else if int32Port, ok := containerPortValue.(int32); ok {
		return int32Port
	} else if float64Port, ok := containerPortValue.(float64); ok {
		return int32(float64Port)
	} else {
		log.Info("Failed to extract value as expected type", "key", key, "value", containerPortValue, "type", fmt.Sprintf("%T", containerPortValue))
		return 0
	}
}

// getSliceOfMaps extracts a slice of maps from a parent map for keys like "containers" or "initContainers"
func getSliceOfMaps(data map[string]interface{}, key string, log logr.Logger) []map[string]interface{} {
	var result []map[string]interface{}
	if data == nil {
		return result
	}

	itemsVal, dataOK := data[key]
	if !dataOK {
		return []map[string]interface{}{} // Return empty non-nil slice
	}

	// First, try to assert if it's already the target type: []map[string]interface{}
	if specificList, ok := itemsVal.([]map[string]interface{}); ok {
		// If it is, we can return it directly (or a copy if you're concerned about modifications)
		// For safety, let's make a copy to avoid modifying the original map's slice.
		result := make([]map[string]interface{}, len(specificList))
		copy(result, specificList)
		return result
	}

	// If not, then try to assert if it's []interface{} and convert each element
	if genericList, ok := itemsVal.([]interface{}); ok {
		var result []map[string]interface{}
		for _, item := range genericList {
			if itemMap, ok := item.(map[string]interface{}); ok {
				result = append(result, itemMap)
			} else {
				log.Info(fmt.Sprintf("Skipping %s entry (item in generic list is not a map)", key), "value", item, "type", fmt.Sprintf("%T", item))
			}
		}
		return result
	}

	// If it's neither of the expected slice types
	log.Info(fmt.Sprintf("Skipping %s (value is not a recognized list type for maps)", key), "value", itemsVal, "type", fmt.Sprintf("%T", itemsVal))
	return []map[string]interface{}{} // Return empty non-nil slice
}

// Helper function to robustly get float64 from an interface{}
// This is the one used for 'averageValue' in Pods metrics.
func getFloat64ValueFromInterface(val interface{}, log logr.Logger) float64 {
	switch v := val.(type) {
	case int:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	case string:
		if parsedFloat, err := strconv.ParseFloat(v, 64); err == nil {
			return parsedFloat
		}
		if quantity, err := resource.ParseQuantity(v); err == nil {
			return float64(quantity.Value()) // Convert quantity to its absolute numerical value in float64
		}
		log.Info("Cannot parse string to float64 or Quantity, returning 0.0", "value", v)
		return 0.0
	default:
		log.Info("Unexpected type for float64 conversion, returning 0.0", "value", val, "type", fmt.Sprintf("%T", val))
		return 0.0
	}
}

// Helper function to robustly get int32 from an interface{}
// This should be in your controller/generic_reconciler.go or a shared utility file.
func getInt32ValueFromInterface(val interface{}, log logr.Logger) int32 {
	switch v := val.(type) {
	case int:
		return int32(v)
	case int32:
		return v
	case int64:
		return int32(v)
	case float64: // JSON numbers often unmarshal to float64
		return int32(v)
	case string: // Try parsing from string if it's a number string
		if parsedInt, err := strconv.ParseInt(v, 10, 32); err == nil {
			return int32(parsedInt)
		}
		if quantity, err := resource.ParseQuantity(v); err == nil {
			return int32(quantity.Value()) // Convert quantity to its absolute numerical value
		}
		log.Info("Cannot parse string to int32, returning 0", "value", v)
		return 0
	default:
		log.Info("Unexpected type for int32 conversion, returning 0", "value", val, "type", fmt.Sprintf("%T", val))
		return 0
	}
}

type MockRegistry struct {
	HasIntegrationFunc    func(gvk schema.GroupVersionKind) bool
	SetIntegrationsFunc   func(integrations []modelv1.IntegrationSpec)
	LockIntegrationsFunc  func() func()
	ListIntegrationsFunc  func() []schema.GroupVersionKind
	GetCopyPathsFunc      func(k schema.GroupVersionKind) []string
	GetTemplatePathsFunc  func(k schema.GroupVersionKind) []string
	GetReferencePathsFunc func(k schema.GroupVersionKind) (map[schema.GroupVersionKind]string, map[schema.GroupVersionKind]string)
	ResolveContextFunc    func(ctx context.Context, resource *unstructured.Unstructured, output map[string]any) error

	// This is the new field and method that was missing
	GetReferenceRulesFunc func(gvk schema.GroupVersionKind) []modelv1.IntegrationApiReferenceSpec

	// lock field is no longer needed in the mock as it's an implementation detail
}

// --- Method Implementations for MockRegistry ---

func (m *MockRegistry) HasIntegration(gvk schema.GroupVersionKind) bool {
	if m.HasIntegrationFunc != nil {
		return m.HasIntegrationFunc(gvk)
	}
	return false
}

func (m *MockRegistry) SetIntegrations(integrations []modelv1.IntegrationSpec) {
	if m.SetIntegrationsFunc != nil {
		m.SetIntegrationsFunc(integrations)
	}
}

func (m *MockRegistry) LockIntegrations() func() {
	if m.LockIntegrationsFunc != nil {
		return m.LockIntegrationsFunc()
	}
	return func() {} // Return a no-op unlock function
}

func (m *MockRegistry) ListIntegrations() []schema.GroupVersionKind {
	if m.ListIntegrationsFunc != nil {
		return m.ListIntegrationsFunc()
	}
	return nil
}

func (m *MockRegistry) GetCopyPaths(k schema.GroupVersionKind) []string {
	if m.GetCopyPathsFunc != nil {
		return m.GetCopyPathsFunc(k)
	}
	return nil
}

func (m *MockRegistry) GetTemplatePaths(k schema.GroupVersionKind) []string {
	if m.GetTemplatePathsFunc != nil {
		return m.GetTemplatePathsFunc(k)
	}
	return nil
}

func (m *MockRegistry) GetReferencePaths(k schema.GroupVersionKind) (map[schema.GroupVersionKind]string, map[schema.GroupVersionKind]string) {
	if m.GetReferencePathsFunc != nil {
		return m.GetReferencePathsFunc(k)
	}
	return nil, nil
}

func (m *MockRegistry) ResolveContext(ctx context.Context, resource *unstructured.Unstructured, output map[string]any) error {
	if m.ResolveContextFunc != nil {
		return m.ResolveContextFunc(ctx, resource, output)
	}
	return nil
}

// This is the implementation for the new method
func (m *MockRegistry) GetReferenceRules(gvk schema.GroupVersionKind) []modelv1.IntegrationApiReferenceSpec {
	if m.GetReferenceRulesFunc != nil {
		return m.GetReferenceRulesFunc(gvk)
	}
	return nil
}

// MockTransformer allows us to control the behavior of the Transformer dependency.
type MockTransformer struct {
	RunFunc      func(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, rClient client.Client, req ctrl.Request, obj *unstructured.Unstructured) ([]*unstructured.Unstructured, error)
	RegistryFunc func() modelv1.RegistryInterface
}

// Run implements the TransformerInterface. It calls the RunFunc field if it's set for a given test.
func (m *MockTransformer) Run(
	ctx context.Context,
	discoveryClient discovery.DiscoveryInterface,
	dynamicClient dynamic.Interface,
	mapper meta.RESTMapper,
	rClient client.Client,
	req ctrl.Request,
	obj *unstructured.Unstructured,
) ([]*unstructured.Unstructured, error) {
	// If the RunFunc field is set for the current test, call it.
	if m.RunFunc != nil {
		return m.RunFunc(ctx, discoveryClient, dynamicClient, rClient, req, obj)
	}
	// Otherwise, return a default value so the code doesn't panic.
	return nil, nil
}

func (m *MockTransformer) Registry() modelv1.RegistryInterface {
	if m.RegistryFunc != nil {
		return m.RegistryFunc()
	}
	return nil
}
