package controller

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp" // Make sure you have this import for logging
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// serviceDiff now compares a cleaned version of the entire spec.
func (r *GenericReconciler) serviceDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
	// Safely get the spec maps without using NestedMap, which can panic.
	existingSpec, ok := existingObj.Object["spec"].(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("existing object spec is not a map[string]interface{}")
	}

	desiredSpec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("desired object spec is not a map[string]interface{}")
	}

	// ✅ NORMALIZE NUMBERS TO INT64 TO PREVENT PANIC
	// We only need to do this for the desired spec, as the existing one from the API server already uses int64.
	normalizedDesiredSpec := normalizeNumbersToInt64(desiredSpec).(map[string]interface{})

	// Create "clean" versions of the specs.
	cleanedExistingSpec := cleanServiceSpec(existingSpec, log)
	cleanedDesiredSpec := cleanServiceSpec(normalizedDesiredSpec, log)

	// Now, compare the two clean maps.
	if !reflect.DeepEqual(cleanedExistingSpec, cleanedDesiredSpec) {
		diff := cmp.Diff(cleanedExistingSpec, cleanedDesiredSpec)
		log.Info("Found a difference in the ServiceSpec", "difference", diff)
		return true, nil
	}

	return false, nil
}

// normalizeNumbersToInt64 recursively finds all 'int' values and converts them to 'int64'.
func normalizeNumbersToInt64(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			v[key] = normalizeNumbersToInt64(val)
		}
		return v
	case []interface{}:
		for i, val := range v {
			v[i] = normalizeNumbersToInt64(val)
		}
		return v
	case int:
		// This is the core conversion
		return int64(v)
	default:
		return v
	}
}

// cleanServiceSpec creates a new map containing only the Service spec fields we want to compare.
func cleanServiceSpec(spec map[string]interface{}, log logr.Logger) map[string]interface{} {
	if spec == nil {
		return nil
	}

	cleanedSpec := make(map[string]interface{})

	// 1. Copy the 'type' field (e.g., "LoadBalancer")
	if serviceType, ok := spec["type"]; ok {
		cleanedSpec["type"] = serviceType
	}

	// 2. Copy the 'selector' field
	if selector, ok := spec["selector"]; ok {
		cleanedSpec["selector"] = selector
	}

	// 3. Get the cleaned 'ports', ignoring system-set fields like 'nodePort'
	cleanedSpec["ports"] = getCleanedServicePorts(spec, log)

	return cleanedSpec
}

// getCleanedServicePorts extracts and cleans only the port information.
func getCleanedServicePorts(spec map[string]interface{}, log logr.Logger) []map[string]interface{} {
	portsInterface, ok := spec["ports"]
	if !ok {
		log.V(1).Info("getCleanedServicePorts - Service ports not found")
		return nil
	}

	ports, ok := portsInterface.([]interface{})
	if !ok {
		log.V(1).Info("getCleanedServicePorts - Service ports not a list")
		return nil
	}

	var servicePorts []map[string]interface{}
	for _, p := range ports {
		if portMap, ok := p.(map[string]interface{}); ok {
			// Create a new, clean map for each port.
			cleanedPort := make(map[string]interface{})

			// Explicitly copy only the fields you control. This ignores 'nodePort'.
			if name, ok := portMap["name"]; ok {
				cleanedPort["name"] = name
			}
			if protocol, ok := portMap["protocol"]; ok {
				cleanedPort["protocol"] = protocol
			}
			if port, ok := portMap["port"]; ok {
				cleanedPort["port"] = port
			}
			if targetPort, ok := portMap["targetPort"]; ok {
				cleanedPort["targetPort"] = targetPort
			}

			servicePorts = append(servicePorts, cleanedPort)
		}
	}

	// Sort the slice of maps by name for a stable comparison.
	sort.SliceStable(servicePorts, func(i, j int) bool {
		nameI, _ := servicePorts[i]["name"].(string)
		nameJ, _ := servicePorts[j]["name"].(string)
		return nameI < nameJ
	})

	return servicePorts
}
