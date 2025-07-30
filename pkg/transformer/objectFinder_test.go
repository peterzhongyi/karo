package transformer

import (
	"context"
	"fmt"
	"sort"
	"testing"

	modelv1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"
	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

func TestFindConnectedResources(t *testing.T) {
	ctx := context.Background()

	// Define GVKs
	deploymentGVK := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	serviceGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"}
	podMonitorGVK := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}

	// Define some test objects
	myDep := newTestObject("apps", "v1", "Deployment", "my-app")
	addReference(myDep, "my-service", "spec.template.spec.serviceName") // Deployment references a Service

	myService := newTestObject("", "v1", "Service", "my-service")

	myPodMonitor := newTestObject("monitoring.coreos.com", "v1", "PodMonitor", "my-app-monitor")
	addReference(myPodMonitor, "my-app", "spec.selector.matchLabels.app") // PodMonitor references the Deployment via a label selector path

	// Mock Instance Cache: This represents the state of the cluster
	mockCache := map[schema.GroupVersionKind]map[string]*unstructured.Unstructured{
		deploymentGVK: {
			getObjectKey(myDep): myDep,
		},
		serviceGVK: {
			getObjectKey(myService): myService,
		},
		podMonitorGVK: {
			getObjectKey(myPodMonitor): myPodMonitor,
		},
	}

	// Mock Registry Rules: Defines how objects reference each other
	mockRefPaths := map[schema.GroupVersionKind][]modelv1.IntegrationApiReferenceSpec{
		deploymentGVK: { // Rule for Deployments
			{
				Group:   "",
				Version: "v1",
				Kind:    "Service",
				Paths:   modelv1.IntegrationApiReferencePathSpec{Name: "spec.template.spec.serviceName"},
			},
		},
		podMonitorGVK: { // Rule for PodMonitors
			{
				// This is a simplified reference, assuming your logic can handle it
				// A real implementation might use label selectors, not direct name paths
				Group:   "apps",
				Version: "v1",
				Kind:    "Deployment",
				Paths:   modelv1.IntegrationApiReferencePathSpec{Name: "spec.selector.matchLabels.app"},
			},
		},
	}

	testCases := []struct {
		name                string
		startObject         *unstructured.Unstructured
		expectedReferenced  []string // list of keys (kind/name)
		expectedReferencing []string // list of keys
		expectError         bool
	}{
		{
			name:                "starting with Deployment, should find Service (referenced) and PodMonitor (referencing)",
			startObject:         myDep,
			expectedReferenced:  []string{"Service/my-service"},
			expectedReferencing: []string{"PodMonitor/my-app-monitor"},
			expectError:         false,
		},
		{
			name:                "starting with Service, should find Deployment (referencing)",
			startObject:         myService,
			expectedReferenced:  []string{},
			expectedReferencing: []string{"Deployment/my-app"},
			expectError:         false,
		},
		{
			name:                "starting with PodMonitor, should find Deployment (referenced)",
			startObject:         myPodMonitor,
			expectedReferenced:  []string{"Deployment/my-app"},
			expectedReferencing: []string{},
			expectError:         false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// ARRANGE
			transformer := &Transformer{
				registry: &mockRegistry{refPaths: mockRefPaths},
				// Inject our mock function
				populateInstanceCacheFunc: func(context.Context, discovery.DiscoveryInterface, dynamic.Interface) (map[schema.GroupVersionKind]map[string]*unstructured.Unstructured, error) {
					if tc.expectError {
						return nil, fmt.Errorf("mock cache population failed")
					}
					return mockCache, nil
				},
			}

			// ACT
			referenced, referencing, err := transformer.findConnectedResources(ctx, nil, nil, tc.startObject)

			// ASSERT
			if tc.expectError {
				if err == nil {
					t.Error("Expected an error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("Received an unexpected error: %v", err)
			}

			// Convert results to sorted key strings for deterministic comparison
			referencedKeys := objectListToKeys(referenced)
			referencingKeys := objectListToKeys(referencing)

			if !cmp.Equal(referencedKeys, tc.expectedReferenced) {
				t.Errorf("Referenced objects mismatch: got %v, want %v", referencedKeys, tc.expectedReferenced)
			}
			if !cmp.Equal(referencingKeys, tc.expectedReferencing) {
				t.Errorf("Referencing objects mismatch: got %v, want %v", referencingKeys, tc.expectedReferencing)
			}
		})
	}
}

// objectListToKeys converts a slice of objects to a sorted slice of their keys for easy comparison.
func objectListToKeys(objs []*unstructured.Unstructured) []string {
	keys := make([]string, len(objs))
	for i, obj := range objs {
		keys[i] = fmt.Sprintf("%s/%s", obj.GetKind(), obj.GetName())
	}
	sort.Strings(keys) // Sort for deterministic comparison
	return keys
}
