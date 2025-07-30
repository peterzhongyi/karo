package transformer

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

func (t *Transformer) findConnectedResources(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, obj *unstructured.Unstructured) (referenced []*unstructured.Unstructured, referencing []*unstructured.Unstructured, err error) {
	var instanceCache map[schema.GroupVersionKind]map[string]*unstructured.Unstructured

	// Use our function field if it's set (it will be in tests).
	// If it's not set (in production), use the real method.
	if t.populateInstanceCacheFunc != nil {
		instanceCache, err = t.populateInstanceCacheFunc(ctx, discoveryClient, dynamicClient)
	} else {
		instanceCache, err = t.populateInstanceCache(ctx, discoveryClient, dynamicClient)
	}

	if err != nil {
		return nil, nil, err
	}

	startObjKey := getObjectKey(obj)
	referencedSet := make(map[string]*unstructured.Unstructured)
	referencingSet := make(map[string]*unstructured.Unstructured)

	for _, instances := range instanceCache {
		for instanceKey, instanceObj := range instances {
			if instanceKey == startObjKey {
				continue
			}

			// Check for parents (does instanceObj reference startObject?)
			namePaths, _ := t.registry.GetReferencePaths(instanceObj.GroupVersionKind())
			for refGVK, namePath := range namePaths {

				if refGVK.Kind == obj.GetKind() && refGVK.Group == obj.GroupVersionKind().Group {
					name, found, _ := unstructured.NestedString(instanceObj.Object, strings.Split(namePath, ".")...)

					if found && name == obj.GetName() {
						referencingSet[instanceKey] = instanceObj
					}
				}
			}

			// Check for children (does startObject reference instanceObj?)
			namePaths, _ = t.registry.GetReferencePaths(obj.GroupVersionKind())
			for refGVK, namePath := range namePaths {

				if refGVK.Kind == instanceObj.GetKind() && refGVK.Group == instanceObj.GroupVersionKind().Group {
					name, found, _ := unstructured.NestedString(obj.Object, strings.Split(namePath, ".")...)

					if found && name == instanceObj.GetName() {
						referencedSet[instanceKey] = instanceObj
					}
				}
			}
		}
	}

	return mapsToList(referencedSet), mapsToList(referencingSet), nil
}

// populateInstanceCache fetches known instances from the cluster.
func (t *Transformer) populateInstanceCache(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface) (map[schema.GroupVersionKind]map[string]*unstructured.Unstructured, error) {
	instanceCache := map[schema.GroupVersionKind]map[string]*unstructured.Unstructured{}
	for _, integrationGVK := range t.registry.ListIntegrations() {
		// First, find the resource name by GVK
		apiResourceList, err := discoveryClient.ServerResourcesForGroupVersion(integrationGVK.GroupVersion().String())
		if err != nil {
			return nil, fmt.Errorf("error getting API resources for group version: %w", err)
		}
		var resourceName string
		for _, apiResource := range apiResourceList.APIResources {
			if apiResource.Kind == integrationGVK.Kind {
				resourceName = apiResource.Name
				break
			}
		}

		// Create a GVR and list the resources
		gvr := integrationGVK.GroupVersion().WithResource(resourceName)
		list, err := dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list resources of type %s: %w", gvr, err)
		}

		instanceMap := map[string]*unstructured.Unstructured{}
		for _, item := range list.Items {
			instanceMap[getObjectKey(&item)] = &item
		}
		instanceCache[integrationGVK] = instanceMap
	}
	return instanceCache, nil
}

// getObjectKey gets an object key.
func getObjectKey(obj *unstructured.Unstructured) string {
	return getKey(obj.GetNamespace(), obj.GetName(), obj.GroupVersionKind())
}

// getKey gets an object key.
func getKey(namespace, name string, gvk schema.GroupVersionKind) string {
	return fmt.Sprintf("%s/%s/%s", namespace, name, gvk.String())
}

// mapsToList converts a map to a lis.
func mapsToList[K comparable, V any](m map[K]V) []V {
	list := make([]V, 0, len(m))
	for _, v := range m {
		list = append(list, v)
	}
	return list
}
