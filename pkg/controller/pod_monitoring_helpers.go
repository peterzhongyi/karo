package controller

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// podMonitoringDiff compares the relevant fields of two PodMonitoring objects.
func (r *GenericReconciler) podMonitoringDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
	existingPM, err := getPodMonitoringDetails(existingObj, log)
	if err != nil {
		return false, fmt.Errorf("error getting existing PodMonitoring details: %w", err)
	}
	newPM, err := getPodMonitoringDetails(obj, log)
	if err != nil {
		return false, fmt.Errorf("error getting new PodMonitoring details: %w", err)
	}

	return !reflect.DeepEqual(existingPM, newPM), nil
}

// getPodMonitoringDetails extracts details from an unstructured object.
func getPodMonitoringDetails(obj *unstructured.Unstructured, log logr.Logger) (*SimplifiedPodMonitoringDetails, error) {
	specInterface, found := obj.Object["spec"]
	if !found {
		log.Info("HPA spec field not found in unstructured object")
		return &SimplifiedPodMonitoringDetails{}, fmt.Errorf("spec field not found in unstructured object")
	}

	spec, ok := specInterface.(map[string]interface{})
	if !ok {
		log.Info("HPA spec is not a map[string]interface{}, returning error: %T, value: %#v", specInterface, specInterface)
		return &SimplifiedPodMonitoringDetails{}, fmt.Errorf("spec is not a map[string]interface{}, got %T", specInterface)
	}

	details := &SimplifiedPodMonitoringDetails{}

	var err error
	// Extract Endpoints
	details.Endpoints, err = extractEndpoints(spec, log)
	if err != nil {
		return nil, err
	}

	// Extract Selector
	details.Selector, err = extractSelector(spec, log)
	if err != nil {
		return nil, err
	}
	// Extract Target Labels
	details.TargetLabels, err = extractTargetLabels(spec, log)
	if err != nil {
		return nil, err
	}

	return details, nil
}

// SimplifiedPodMonitoringDetails holds only the fields we care about for comparison.
type SimplifiedPodMonitoringDetails struct {
	Endpoints    []map[string]interface{} // Example: list of endpoints as maps
	Selector     map[string]string        // Example: matchLabels
	TargetLabels map[string][]string      // Add other top-level fields you care about
}

// extractEndpoints extracts and normalizes endpoints information.
func extractEndpoints(spec map[string]interface{}, log logr.Logger) ([]map[string]interface{}, error) {
	endpointsInterface, found := spec["endpoints"]
	if !found {
		return nil, nil // It's ok if endpoints are not found
	}

	endpointsList, ok := endpointsInterface.([]interface{})
	if !ok {
		log.Info("endpoints is not a []interface{}, skipping: %T", endpointsInterface)
		return nil, fmt.Errorf("endpoints is not a []interface{}")
	}

	extractedEndpoints := make([]map[string]interface{}, 0)
	for _, ep := range endpointsList {
		epMap, ok := ep.(map[string]interface{})
		if !ok {
			log.Info("endpoint is not a map[string]interface{}, skipping: %T", ep)
			continue
		}

		normalizedEpMap := make(map[string]interface{})
		for k, v := range epMap {
			switch k {
			case "port":
				normalizedEpMap[k] = fmt.Sprintf("%v", v)
			case "interval", "path":
				normalizedEpMap[k] = fmt.Sprintf("%v", v)
			// Add other fields you care about (e.g., scheme, bearerTokenSecret)
			default:
				normalizedEpMap[k] = v // Copy as-is
			}
		}
		extractedEndpoints = append(extractedEndpoints, normalizedEpMap)
	}
	return extractedEndpoints, nil
}

// extractTargetLabels extracts and normalizes target labels information.
func extractTargetLabels(spec map[string]interface{}, log logr.Logger) (map[string][]string, error) {
	targetLabelsInterface, found := spec["targetLabels"]
	if !found {
		return nil, nil // It's ok if targetLabels are not found
	}

	targetLabelsMap, ok := targetLabelsInterface.(map[string]interface{})
	if !ok {
		log.Info("targetLabels is not a map[string]interface{}, skipping: %T", targetLabelsInterface)
		return nil, fmt.Errorf("targetLabels is not a map[string]interface{}")
	}

	if targetLabelsMap == nil {
		return make(map[string][]string), nil
	}

	stringTargetLabels := make(map[string][]string)
	metadataLabels, foundMetadata := targetLabelsMap["metadata"]
	if foundMetadata {
		metadataLabelsList, ok := metadataLabels.([]interface{})
		if !ok {
			log.Info("metadata labels is not a []interface{}, skipping: %T", metadataLabels)
			return nil, fmt.Errorf("metadata labels is not a []interface{}")
		}

		var stringMetadataLabels []string
		for _, item := range metadataLabelsList {
			if s, ok := item.(string); ok {
				stringMetadataLabels = append(stringMetadataLabels, s)
			} else {
				log.Info("metadata label value not a string, skipping: %v, type: %T", item, item)
			}
		}
		sort.Strings(stringMetadataLabels)
		stringTargetLabels["metadata"] = stringMetadataLabels
	}

	return stringTargetLabels, nil
}

// extractSelector extracts and normalizes selector information.
func extractSelector(spec map[string]interface{}, log logr.Logger) (map[string]string, error) {
	selectorInterface, found := spec["selector"]
	if !found {
		return nil, nil // It's ok if selector is not found
	}

	selectorMap, ok := selectorInterface.(map[string]interface{})
	if !ok {
		log.Info("selector is not a map[string]interface{}, skipping: %T", selectorInterface)
		return nil, fmt.Errorf("selector is not a map[string]interface{}")
	}

	matchLabelsValue, foundMatchLabels := selectorMap["matchLabels"]
	if !foundMatchLabels {
		return nil, nil // It's ok if matchLabels is not found
	}

	if matchLabelsValue == nil {
		return make(map[string]string), nil
	}

	matchLabels, ok := matchLabelsValue.(map[string]interface{})
	if !ok {
		log.Info("matchLabels is not a map[string]interface{}, skipping: %T", matchLabelsValue)
		return nil, fmt.Errorf("matchLabels is not a map[string]interface{}")
	}

	stringMatchLabels := make(map[string]string)
	for k, v := range matchLabels {
		if s, ok := v.(string); ok {
			stringMatchLabels[k] = s
		} else {
			log.Info("Selector matchLabel value not a string, skipping key: %s, value: %v, type: %T", k, v, v)
		}
	}
	return stringMatchLabels, nil
}
