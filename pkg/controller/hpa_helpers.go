package controller

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// hpaDiff compares the relevant fields of two HorizontalPodAutoscaler objects.
func (r *GenericReconciler) hpaDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
	existingHPA, err := getHPADetails(existingObj, log)
	if err != nil {
		return false, fmt.Errorf("error getting existing HPA details: %w", err)
	}
	newHPA, err := getHPADetails(obj, log)
	if err != nil {
		return false, fmt.Errorf("error getting new HPA details: %w", err)
	}
	return !reflect.DeepEqual(existingHPA, newHPA), nil
}

// SimplifiedHPADetails holds only the fields we care about for HPA comparison.
// You'd define nested structs for metrics if you want to be more specific.
type SimplifiedHPADetails struct {
	MinReplicas int32
	MaxReplicas int32
	Metrics     []map[string]interface{} // Use map[string]interface{} to represent metrics for DeepEqual
	// Add other fields you care about, e.g., ScaleTargetRef (name only)
}

func getHPADetails(obj *unstructured.Unstructured, log logr.Logger) (*SimplifiedHPADetails, error) {
	if obj == nil || obj.Object == nil {
		log.Info("getHPADetails received nil object or object.Object is nil")
		return &SimplifiedHPADetails{Metrics: []map[string]interface{}{}}, fmt.Errorf("input object is nil or its data is nil")
	}

	specInterface, found := obj.Object["spec"]
	if !found {
		log.Info("HPA spec field not found in unstructured object")
		// Return an empty/default structure if spec is not found
		return nil, fmt.Errorf("spec field not found in unstructured object")
	}

	spec, ok := specInterface.(map[string]interface{})
	if !ok {
		// This means obj.Object["spec"] was NOT a map[string]interface{}
		// but some other primitive type (like int, as indicated by the panic).
		// This is a malformed HPA object.
		log.Error(nil, "HPA spec is not a map[string]interface{}, returning error", "specType", fmt.Sprintf("%T", specInterface), "specValue", fmt.Sprintf("%#v", specInterface))
		return &SimplifiedHPADetails{Metrics: []map[string]interface{}{}},
			fmt.Errorf("HPA spec is not a map[string]interface{}, got %T", specInterface)
	}

	details := &SimplifiedHPADetails{
		MinReplicas: getInt32Value(spec, "minReplicas", log),
		MaxReplicas: getInt32Value(spec, "maxReplicas", log),
		Metrics:     getMetricsFromHPA(spec, log),
	}

	// You might also want to include scaleTargetRef.name for comparison
	// if it can change and should trigger an update.
	// scaleTargetRefName, _, _ := unstructured.NestedString(spec, "scaleTargetRef", "name")
	// details.ScaleTargetRefName = scaleTargetRefName

	return details, nil
}

// getMetricsFromHPA extracts and normalizes metrics slice from HPA spec
// without using runtime.DeepCopyJSON or direct shallow copies of complex types.
// It explicitly constructs the map for comparison.
func getMetricsFromHPA(spec map[string]interface{}, log logr.Logger) []map[string]interface{} {
	metricsInterface, found := spec["metrics"]
	if !found {
		return []map[string]interface{}{} // Return empty slice, not nil
	}
	metricsList, ok := metricsInterface.([]interface{})
	if !ok {
		log.Info("HPA metrics not a list, unexpected type", "metrics", metricsInterface, "type", fmt.Sprintf("%T", metricsInterface))
		return []map[string]interface{}{}
	}

	extractedMetrics := make([]map[string]interface{}, 0, len(metricsList))
	for _, m := range metricsList {
		if metricMap, ok := m.(map[string]interface{}); ok {
			normalizedMetricMap := make(map[string]interface{}) // Create a new, empty map for this metric

			// Extract and normalize 'type' field (common to all metrics)
			typeVal, typeFound := metricMap["type"].(string)
			if typeFound {
				normalizedMetricMap["type"] = typeVal // Store the string type
			} else {
				log.Info("HPA metric missing 'type' field, skipping", "metricMap", metricMap)
				continue // Skip if metric type is unknown
			}

			// Extract and normalize fields based on metric type
			switch typeVal {
			case "Pods":
				podsMetricsMap := make(map[string]interface{})
				if podsInterface, foundPods := metricMap["pods"]; foundPods {
					if podsData, okPods := podsInterface.(map[string]interface{}); okPods {
						metricInnerMap := make(map[string]interface{})
						if metricInterface, foundInnerMetric := podsData["metric"]; foundInnerMetric {
							if metricData, okInnerMetric := metricInterface.(map[string]interface{}); okInnerMetric {
								if nameVal, okName := metricData["name"].(string); okName {
									metricInnerMap["name"] = nameVal
								}
							}
						}
						podsMetricsMap["metric"] = metricInnerMap

						targetInnerMap := make(map[string]interface{})
						if targetInterface, foundTarget := podsData["target"]; foundTarget {
							if targetData, okTarget := targetInterface.(map[string]interface{}); okTarget {
								if typeTargetVal, okTypeTarget := targetData["type"].(string); okTypeTarget {
									targetInnerMap["type"] = typeTargetVal
								}
								if avgValue, okAvgValue := targetData["averageValue"]; okAvgValue {
									// Normalize averageValue to string for Pods type
									targetInnerMap["averageValue"] = fmt.Sprintf("%v", getFloat64ValueFromInterface(avgValue, log))
								}
							}
						}
						podsMetricsMap["target"] = targetInnerMap
					}
				}
				normalizedMetricMap["pods"] = podsMetricsMap

			case "Resource":
				resourceMetricsMap := make(map[string]interface{})
				if resourceInterface, foundResource := metricMap["resource"]; foundResource {
					if resourceData, okResource := resourceInterface.(map[string]interface{}); okResource {
						if nameVal, okName := resourceData["name"].(string); okName {
							resourceMetricsMap["name"] = nameVal
						}

						targetInnerMap := make(map[string]interface{})
						if targetInterface, foundTarget := resourceData["target"]; foundTarget {
							if targetData, okTarget := targetInterface.(map[string]interface{}); okTarget {
								if typeTargetVal, okTypeTarget := targetData["type"].(string); okTypeTarget {
									targetInnerMap["type"] = typeTargetVal
								}
								if avgUtilization, okAvgUtilization := targetData["averageUtilization"]; okAvgUtilization {
									// Normalize averageUtilization to int32 for Resource type
									targetInnerMap["averageUtilization"] = getInt32ValueFromInterface(avgUtilization, log)
								}
							}
						}
						resourceMetricsMap["target"] = targetInnerMap
					}
				}
				normalizedMetricMap["resource"] = resourceMetricsMap

			// Add cases for "Object" and "External" if you plan to support them
			// These would require similar explicit manual copying and type normalization for their specific fields.
			default:
				log.Info("Unsupported HPA metric type encountered, skipping normalization for specific fields", "type", typeVal)
				// If you want to include unrecognized types in diffing, you'd copy more fields here manually.
				// But for strict comparison, only copying known fields is safer.
			}

			extractedMetrics = append(extractedMetrics, normalizedMetricMap)
		} else {
			log.Info("HPA metric entry is not a map, skipping", "metric", m, "type", fmt.Sprintf("%T", m))
		}
	}

	// Sort metrics slice to ensure consistent order for DeepEqual
	// Sort by metric type first, then by the specific metric name.
	sort.Slice(extractedMetrics, func(i, j int) bool {
		typeI, _ := extractedMetrics[i]["type"].(string)
		typeJ, _ := extractedMetrics[j]["type"].(string)

		if typeI != typeJ {
			return typeI < typeJ // Sort by metric type (e.g., "Pods" before "Resource")
		}

		// Then, sort by specific metric name based on its type
		var nameI, nameJ string
		if typeI == "Pods" {
			// Safely extract name for sorting
			if podsMap, ok := extractedMetrics[i]["pods"].(map[string]interface{}); ok {
				if metricMap, ok := podsMap["metric"].(map[string]interface{}); ok {
					nameI, _ = metricMap["name"].(string)
				}
			}
			if podsMap, ok := extractedMetrics[j]["pods"].(map[string]interface{}); ok {
				if metricMap, ok := podsMap["metric"].(map[string]interface{}); ok {
					nameJ, _ = metricMap["name"].(string)
				}
			}
		} else if typeI == "Resource" {
			// Safely extract name for sorting
			if resourceMap, ok := extractedMetrics[i]["resource"].(map[string]interface{}); ok {
				nameI, _ = resourceMap["name"].(string)
			}
			if resourceMap, ok := extractedMetrics[j]["resource"].(map[string]interface{}); ok {
				nameJ, _ = resourceMap["name"].(string)
			}
		}
		// Add sorting for other types if necessary (e.g., Object, External)
		// using their specific nested paths.

		return nameI < nameJ
	})

	return extractedMetrics
}
