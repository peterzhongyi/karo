package controller

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// jobDiff compares the spec of two Job objects.
func (r *GenericReconciler) jobDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
	// Extract the relevant details from the existing Job
	existingSA, existingInitContainers, existingContainers, err := getJobPodSpecDetails(existingObj, log)
	if err != nil {
		return false, fmt.Errorf("error getting existing job pod spec details: %w", err)
	}

	// Extract the relevant details from the new (desired) Job
	newSA, newInitContainers, newContainers, err := getJobPodSpecDetails(obj, log)
	if err != nil {
		return false, fmt.Errorf("error getting new job pod spec details: %w", err)
	}

	// Compare serviceAccountName
	if existingSA != newSA {
		log.Info("Job diff: serviceAccountName changed", "old", existingSA, "new", newSA)
		return true, nil
	}

	// Compare InitContainers
	if len(existingInitContainers) != len(newInitContainers) {
		log.Info("Job diff: number of initContainers changed", "oldCount", len(existingInitContainers), "newCount", len(newInitContainers))
		return true, nil
	}
	for i := range existingInitContainers {
		// DeepEqual works well on the SimplifiedContainerSpec struct as it contains
		// only the fields you care about for comparison, and their types (slices, maps, structs)
		// are handled by DeepEqual.
		if !reflect.DeepEqual(existingInitContainers[i], newInitContainers[i]) {
			log.Info("Job diff: initContainer spec changed", "index", i, "old", existingInitContainers[i], "new", newInitContainers[i])
			return true, nil
		}
	}

	// Compare Containers (main containers)
	if len(existingContainers) != len(newContainers) {
		log.Info("Job diff: number of containers changed", "oldCount", len(existingContainers), "newCount", len(newContainers))
		return true, nil
	}
	for i := range existingContainers {
		if !reflect.DeepEqual(existingContainers[i], newContainers[i]) {
			return true, nil
		}
	}

	// If none of the cared-about fields differ, return false (no diff)
	return false, nil
}

func getJobPodSpecDetails(obj *unstructured.Unstructured, log logr.Logger) (string, []SimplifiedContainerSpec, []SimplifiedContainerSpec, error) {
	podSpecMap, err := getNestedMap(obj.Object, "spec", "template", "spec")
	if err != nil {
		// Log a warning if paths don't exist, but don't error out completely
		// as some Jobs might not have all these fields explicitly.
		log.Info("Could not find Job PodSpec details, treating as empty for comparison", "error", err)
		return "", nil, nil, nil
	}

	serviceAccountName := getStringValue(podSpecMap, "serviceAccountName")

	initContainersList := getSliceOfMaps(podSpecMap, "initContainers", log)
	initContainers := []SimplifiedContainerSpec{}
	for _, cMap := range initContainersList {
		initContainers = append(initContainers, extractSimplifiedContainerSpec(cMap, log))
	}

	containersList := getSliceOfMaps(podSpecMap, "containers", log)
	containers := []SimplifiedContainerSpec{}
	for _, cMap := range containersList {
		containers = append(containers, extractSimplifiedContainerSpec(cMap, log))
	}

	sort.Slice(containers, func(i, j int) bool {
		return containers[i].Name < containers[j].Name
	})

	return serviceAccountName, initContainers, containers, nil
}

// extractSimplifiedContainerSpec extracts only the specified relevant fields
func extractSimplifiedContainerSpec(containerMap map[string]interface{}, log logr.Logger) SimplifiedContainerSpec {
	return SimplifiedContainerSpec{
		Name:         getStringValue(containerMap, "name"), // Include name for better debugging, though not strictly for diffing logic
		Args:         getStringList(containerMap, "args"),
		Env:          getEnvVars(containerMap),      // This needs to handle ValueFrom correctly
		VolumeMounts: getVolumeMounts(containerMap), // This needs to handle ReadOnly correctly
		Resources:    getResources(containerMap, log),
	}
}
