package transformer

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// topologicalSort sorts the given unstructured objects based on their dependencies
// as defined in the IntegrationRegistry.
func (t *Transformer) topologicalSort(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	inDegree := map[string]int{}
	adjacencyList := map[string][]string{}
	objectMap := map[string]*unstructured.Unstructured{}

	// Initialize in-degree and adjacency list.
	for _, obj := range objects {
		key := getObjectKey(obj)
		objectMap[key] = obj
		inDegree[key] = 0
		adjacencyList[key] = []string{}
	}

	// Build dependency graph.
	for _, obj := range objects {
		key := getObjectKey(obj)
		referencedKeys, err := t.getReferencedObjectKeys(obj, objectMap)
		if err != nil {
			return nil, err
		}
		for _, refKey := range referencedKeys {
			inDegree[refKey]++
			adjacencyList[key] = append(adjacencyList[key], refKey)
		}
	}

	// Find sources (nodes with in-degree 0).
	sources := []string{}
	for key, degree := range inDegree {
		if degree == 0 {
			sources = append(sources, key)
		}
	}

	// Perform topological sort.
	sortedList := []*unstructured.Unstructured{}
	for len(sources) > 0 {
		sourceKey := sources[0]
		sources = sources[1:]
		sortedList = append(sortedList, objectMap[sourceKey])

		for _, dependentKey := range adjacencyList[sourceKey] {
			inDegree[dependentKey]--
			if inDegree[dependentKey] == 0 {
				sources = append(sources, dependentKey)
			}
		}
	}

	// Check for cycles.
	if len(sortedList) != len(objects) {
		return nil, fmt.Errorf("detected a cycle in the dependencies")
	}

	// reverse the list
	for i, j := 0, len(sortedList)-1; i < j; i, j = i+1, j-1 {
		sortedList[i], sortedList[j] = sortedList[j], sortedList[i]
	}

	return sortedList, nil
}

// getReferencedObjectKeys returns the keys of the objects referenced by the given object.
func (t *Transformer) getReferencedObjectKeys(obj *unstructured.Unstructured, objectMap map[string]*unstructured.Unstructured) ([]string, error) {
	referencedKeys := []string{}
	gvk := obj.GroupVersionKind()
	namePaths, namespacePaths := t.registry.GetReferencePaths(gvk)

	for refGVK, namePath := range namePaths {
		name, found, err := unstructured.NestedString(obj.Object, strings.Split(namePath, ".")...)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("name not found for path %q", namePath)
		}
		namespace := "default"
		if namespacePath, ok := namespacePaths[refGVK]; ok {
			namespace, found, err = unstructured.NestedString(obj.Object, strings.Split(namespacePath, ".")...)
			if err != nil {
				return nil, err
			}
			if !found {
				namespace = "default"
			}
		}
		refKey := getKey(namespace, name, refGVK)
		if _, ok := objectMap[refKey]; ok {
			referencedKeys = append(referencedKeys, refKey)
		}
	}
	return referencedKeys, nil
}
