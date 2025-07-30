// In controller/generic_controller_test.go
package controller

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestDeploymentDiff(t *testing.T) {
	logger := testLogger()    // Assuming you have this helper
	r := &GenericReconciler{} // Create an instance to call the method

	// Base container spec for less verbosity
	baseImage := "nginx:1.20"
	baseContainerName := "web"
	baseSA := "sa-default"
	containerDefault := detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, nil, nil, "")
	defaultContainersList := []map[string]interface{}{containerDefault}

	// Define some common container configurations
	nginxContainer1_20 := simpleContainerMap("nginx", "nginx:1.20", nil, nil, nil)

	cpuLimit := resource.MustParse("200m")
	nginxContainerWithResources := simpleContainerMap("nginx", "nginx:1.20", nil, nil, &corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": cpuLimit}})
	nginxContainerDifferentResources := simpleContainerMap("nginx", "nginx:1.20", nil, nil, &corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("300m")}})

	type testCase struct {
		name        string
		existingDep *unstructured.Unstructured
		desiredDep  *unstructured.Unstructured
		expectDiff  bool
	}
	testCases := []testCase{
		{
			name: "identical Deployments with more fields",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{
				detailedContainerMap(baseContainerName, baseImage, []string{"start"},
					[]corev1.EnvVar{{Name: "E1", Value: "V1"}},
					[]corev1.ContainerPort{{Name: "http", ContainerPort: 80}},
					[]corev1.VolumeMount{{Name: "cache", MountPath: "/cache"}},
					&corev1.ResourceRequirements{Requests: corev1.ResourceList{"cpu": resource.MustParse("100m")}},
					corev1.PullIfNotPresent,
				),
			}, baseSA, nil, nil),
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{
				detailedContainerMap(baseContainerName, baseImage, []string{"start"},
					[]corev1.EnvVar{{Name: "E1", Value: "V1"}},
					[]corev1.ContainerPort{{Name: "http", ContainerPort: 80}},
					[]corev1.VolumeMount{{Name: "cache", MountPath: "/cache"}},
					&corev1.ResourceRequirements{Requests: corev1.ResourceList{"cpu": resource.MustParse("100m")}},
					corev1.PullIfNotPresent,
				),
			}, baseSA, nil, nil),
			expectDiff: false,
		},
		{
			name:        "identical Deployments",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20}, "sa-default", nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20}, "sa-default", nil, nil),
			expectDiff:  false,
		},
		{
			name:        "different number of replicas",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20}, "sa-default", nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 2, []map[string]interface{}{nginxContainer1_20}, "sa-default", nil, nil),
			expectDiff:  false,
		},
		{
			name:        "different container image",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{containerDefault}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, "nginx:1.21", nil, nil, nil, nil, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different container args",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, []string{"start"}, nil, nil, nil, nil, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, []string{"run"}, nil, nil, nil, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different container env var value",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, []corev1.EnvVar{{Name: "ENV_VAR", Value: "val1"}}, nil, nil, nil, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, []corev1.EnvVar{{Name: "ENV_VAR", Value: "val2"}}, nil, nil, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "added container env var",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, []corev1.EnvVar{{Name: "E1", Value: "V1"}}, nil, nil, nil, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, []corev1.EnvVar{{Name: "E1", Value: "V1"}, {Name: "E2", Value: "V2"}}, nil, nil, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different container resources",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainerWithResources}, "sa-default", nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainerDifferentResources}, "sa-default", nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different container port number",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, []corev1.ContainerPort{{Name: "http", ContainerPort: 80}}, nil, nil, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, []corev1.ContainerPort{{Name: "http", ContainerPort: 81}}, nil, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different volume mount path",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, []corev1.VolumeMount{{Name: "cache", MountPath: "/cache"}}, nil, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, []corev1.VolumeMount{{Name: "cache", MountPath: "/data"}}, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different container resource limits",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, nil, &corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("100m")}}, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, nil, &corev1.ResourceRequirements{Limits: corev1.ResourceList{"cpu": resource.MustParse("200m")}}, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different volume mount readOnly",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, []corev1.VolumeMount{{Name: "cache", MountPath: "/cache", ReadOnly: false}}, nil, "")}, baseSA, nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{detailedContainerMap(baseContainerName, baseImage, nil, nil, nil, []corev1.VolumeMount{{Name: "cache", MountPath: "/cache", ReadOnly: true}}, nil, "")}, baseSA, nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different serviceAccountName",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20}, "sa-default", nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20}, "sa-other", nil, nil),
			expectDiff:  true,
		},
		{
			name:        "different number of containers",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20}, "sa-default", nil, nil),
			desiredDep:  newUnstructuredDeployment(t, "test-dep", 1, []map[string]interface{}{nginxContainer1_20, simpleContainerMap("sidecar", "busybox", nil, nil, nil)}, "sa-default", nil, nil),
			expectDiff:  true,
		},
		{
			name: "identical deployments with one volume",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("cache", emptyDirVolumeSourceMap())}),
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("cache", emptyDirVolumeSourceMap())}),
			expectDiff: false,
		},
		{
			name: "different volume name",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("cache-old", emptyDirVolumeSourceMap())}),
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("cache-new", emptyDirVolumeSourceMap())}),
			expectDiff: true,
		},
		{
			name: "different volume source type",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("data", emptyDirVolumeSourceMap())}),
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("data", secretVolumeSourceMap("my-secret"))}),
			expectDiff: true,
		},
		{
			name: "different secret name in secret volume source",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("data", secretVolumeSourceMap("my-secret-v1"))}),
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("data", secretVolumeSourceMap("my-secret-v2"))}),
			expectDiff: true,
		},
		{
			name:        "one volume added",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil, nil), // No volumes
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("cache", emptyDirVolumeSourceMap())}),
			expectDiff: true,
		},
		{
			name: "one volume removed",
			existingDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("cache", emptyDirVolumeSourceMap())}),
			desiredDep: newUnstructuredDeployment(t, "test-dep", 1, defaultContainersList, baseSA, nil, nil), // No volumes
			expectDiff: true,
		},
		{
			name: "identical deployments with one CSI volume",
			existingDep: newUnstructuredDeployment(t, "test-dep-csi1", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("csi-vol", csiVolumeSourceMap("csi.driver.example.com", boolPtr(true), map[string]string{"param1": "val1"}))}),
			desiredDep: newUnstructuredDeployment(t, "test-dep-csi1", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("csi-vol", csiVolumeSourceMap("csi.driver.example.com", boolPtr(true), map[string]string{"param1": "val1"}))}),
			expectDiff: false,
		},
		{
			name: "different CSI volume driver",
			existingDep: newUnstructuredDeployment(t, "test-dep-csi2", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("csi-vol", csiVolumeSourceMap("driver-A.com", boolPtr(false), nil))}),
			desiredDep: newUnstructuredDeployment(t, "test-dep-csi2", 1, defaultContainersList, baseSA, nil,
				[]map[string]interface{}{volumeMap("csi-vol", csiVolumeSourceMap("driver-B.com", boolPtr(false), nil))}),
			expectDiff: true,
		},
		{
			name: "semantically equal resource quantities should not cause a diff",
			existingDep: newUnstructuredDeployment(t, "test-dep-semantic", 1,
				[]map[string]interface{}{
					// This container represents what might exist in the cluster
					detailedContainerMap("web", "nginx", nil, nil, nil, nil,
						&corev1.ResourceRequirements{
							Requests: corev1.ResourceList{"cpu": resource.MustParse("1000m")},
						},
						"",
					),
				},
				"sa-default", nil, nil),
			desiredDep: newUnstructuredDeployment(t, "test-dep-semantic", 1,
				[]map[string]interface{}{
					// This container represents what your template generates
					detailedContainerMap("web", "nginx", nil, nil, nil, nil,
						&corev1.ResourceRequirements{
							Requests: corev1.ResourceList{"cpu": resource.MustParse("1")},
						},
						"",
					),
				},
				"sa-default", nil, nil),
			// This is the key assertion. We expect NO diff.
			expectDiff: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Ensure you have a deploymentDiff method on r (*GenericReconciler)
			diff, err := r.deploymentDiff(tc.existingDep, tc.desiredDep, logger)
			if err != nil {
				t.Fatalf("deploymentDiff returned an unexpected error: %v", err)
			}
			if diff != tc.expectDiff {
				t.Errorf("deploymentDiff() = %v, want %v for case '%s'", diff, tc.expectDiff, tc.name)
			}
		})
	}
}

func TestCanonicalizeResourceQuantity(t *testing.T) {
	tests := []struct {
		name         string
		resourceName corev1.ResourceName
		input        resource.Quantity
		expected     resource.Quantity
	}{
		{
			name:         "CPU - whole number",
			resourceName: corev1.ResourceCPU,
			input:        resource.MustParse("1"),
			expected:     *resource.NewMilliQuantity(1000, resource.DecimalSI),
		},
		{
			name:         "CPU - millicores",
			resourceName: corev1.ResourceCPU,
			input:        resource.MustParse("500m"),
			expected:     *resource.NewMilliQuantity(500, resource.DecimalSI),
		},
		{
			name:         "CPU - float",
			resourceName: corev1.ResourceCPU,
			input:        resource.MustParse("1.5"),
			expected:     *resource.NewMilliQuantity(1500, resource.DecimalSI),
		},
		{
			name:         "Memory - Gi",
			resourceName: corev1.ResourceMemory,
			input:        resource.MustParse("1Gi"),
			expected:     *resource.NewQuantity(1024*1024*1024, resource.BinarySI),
		},
		{
			name:         "Memory - Mi",
			resourceName: corev1.ResourceMemory,
			input:        resource.MustParse("512Mi"),
			expected:     *resource.NewQuantity(512*1024*1024, resource.BinarySI),
		},
		{
			name:         "GPU - nvidia.com/gpu",
			resourceName: "nvidia.com/gpu",
			input:        resource.MustParse("1"),
			expected:     *resource.NewQuantity(1, resource.BinarySI),
		},
		{
			name:         "Unknown resource",
			resourceName: "example.com/foo",
			input:        resource.MustParse("10"),
			expected:     resource.MustParse("10"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := canonicalizeResourceQuantity(tt.resourceName, tt.input)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("canonicalizeResourceQuantity() = %v, want %v", actual, tt.expected)
			}
		})
	}
}

func simpleContainerMap(name, image string, args []string, env []corev1.EnvVar, resources *corev1.ResourceRequirements) map[string]interface{} {
	container := map[string]interface{}{
		"name":  name,
		"image": image,
	}
	if args != nil {
		interfaceArgs := make([]interface{}, len(args))
		for i, v := range args {
			interfaceArgs[i] = v
		}
		container["args"] = interfaceArgs
	}
	if env != nil {
		envMaps := make([]interface{}, len(env))
		for i, e := range env {
			envMap := map[string]interface{}{"name": e.Name}
			if e.Value != "" {
				envMap["value"] = e.Value
			}
			// Add ValueFrom if your diff cares about it
			envMaps[i] = envMap
		}
		container["env"] = envMaps
	}
	if resources != nil {
		resMap := map[string]interface{}{}
		if len(resources.Limits) > 0 {
			limits := map[string]interface{}{}
			for k, v := range resources.Limits {
				limits[string(k)] = v.String()
			}
			resMap["limits"] = limits
		}
		if len(resources.Requests) > 0 {
			requests := map[string]interface{}{}
			for k, v := range resources.Requests {
				requests[string(k)] = v.String()
			}
			resMap["requests"] = requests
		}
		if len(resMap) > 0 {
			container["resources"] = resMap
		}
	}
	return container
}

func newUnstructuredDeployment(
	t *testing.T,
	name string,
	replicas int32,
	containersData []map[string]interface{},
	serviceAccountName string,
	podLabels map[string]string,
	volumesData []map[string]interface{},
) *unstructured.Unstructured {
	if podLabels == nil {
		podLabels = map[string]string{"app": name} // Default pod label
	}

	podSpec := map[string]interface{}{
		"containers": containersData,
	}
	if serviceAccountName != "" {
		podSpec["serviceAccountName"] = serviceAccountName
	}
	if volumesData != nil {
		podSpec["volumes"] = volumesData
	}

	deployment := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
			"spec": map[string]interface{}{
				"replicas": replicas,
				"selector": map[string]interface{}{
					"matchLabels": podLabels, // Selector should match pod template labels
				},
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": podLabels,
					},
					"spec": podSpec,
				},
			},
		},
	}
	return deployment
}

func detailedContainerMap(
	name, image string,
	args []string,
	env []corev1.EnvVar,
	ports []corev1.ContainerPort,
	volumeMounts []corev1.VolumeMount,
	resources *corev1.ResourceRequirements,
	imagePullPolicy corev1.PullPolicy,
) map[string]interface{} {
	container := map[string]interface{}{
		"name":  name,
		"image": image,
	}
	if args != nil {
		interfaceArgs := make([]interface{}, len(args))
		for i, v := range args {
			interfaceArgs[i] = v
		}
		container["args"] = interfaceArgs
	}
	if env != nil {
		envMaps := make([]interface{}, len(env))
		for i, e := range env {
			envMap := map[string]interface{}{"name": e.Name}
			if e.Value != "" {
				envMap["value"] = e.Value
			}
			// Basic support for ValueFrom - extend if your diff is more detailed
			if e.ValueFrom != nil {
				if e.ValueFrom.SecretKeyRef != nil {
					envMap["valueFrom"] = map[string]interface{}{
						"secretKeyRef": map[string]interface{}{
							"name": e.ValueFrom.SecretKeyRef.Name,
							"key":  e.ValueFrom.SecretKeyRef.Key,
						},
					}
				} else if e.ValueFrom.ConfigMapKeyRef != nil {
					envMap["valueFrom"] = map[string]interface{}{
						"configMapKeyRef": map[string]interface{}{
							"name": e.ValueFrom.ConfigMapKeyRef.Name,
							"key":  e.ValueFrom.ConfigMapKeyRef.Key,
						},
					}
				}
				// Add FieldRef, ResourceFieldRef if needed
			}
			envMaps[i] = envMap
		}
		container["env"] = envMaps
	}
	if ports != nil {
		container["ports"] = containerPortsToMaps(ports)
	}
	if volumeMounts != nil {
		container["volumeMounts"] = volumeMountsToMaps(volumeMounts)
	}
	if resources != nil {
		resMap := map[string]interface{}{}
		if len(resources.Limits) > 0 {
			limits := map[string]interface{}{}
			for k, v := range resources.Limits {
				limits[string(k)] = v.String()
			}
			resMap["limits"] = limits
		}
		if len(resources.Requests) > 0 {
			requests := map[string]interface{}{}
			for k, v := range resources.Requests {
				requests[string(k)] = v.String()
			}
			resMap["requests"] = requests
		}
		if len(resMap) > 0 {
			container["resources"] = resMap
		}
	}
	if imagePullPolicy != "" {
		container["imagePullPolicy"] = string(imagePullPolicy)
	}
	// Probes (Liveness, Readiness, Startup) and SecurityContext can be added here similarly
	// For probes, you'd compare fields like httpGet.path, exec.command, initialDelaySeconds, etc.
	// For securityContext, fields like runAsUser, runAsNonRoot, capabilities.
	return container
}

// Helper for corev1.VolumeMount to map
func volumeMountsToMaps(mounts []corev1.VolumeMount) []interface{} {
	if mounts == nil {
		return nil
	}
	maps := make([]interface{}, len(mounts))
	for i, vm := range mounts {
		mountMap := map[string]interface{}{
			"name":      vm.Name,
			"mountPath": vm.MountPath,
		}
		if vm.ReadOnly {
			mountMap["readOnly"] = vm.ReadOnly
		}
		if vm.SubPath != "" {
			mountMap["subPath"] = vm.SubPath
		}
		// MountPropagation and SubPathExpr could be added if your diff checks them
		maps[i] = mountMap
	}
	return maps
}

func containerPortsToMaps(ports []corev1.ContainerPort) []interface{} {
	if ports == nil {
		return nil
	}
	maps := make([]interface{}, len(ports))
	for i, p := range ports {
		portMap := map[string]interface{}{
			"containerPort": p.ContainerPort,
		}
		if p.Name != "" {
			portMap["name"] = p.Name
		}
		if p.Protocol != "" {
			portMap["protocol"] = string(p.Protocol) // Protocol is a typed string
		}
		// HostIP and HostPort are less common to diff but could be added
		maps[i] = portMap
	}
	return maps
}

// Helper to create a map for a volume for test data
func volumeMap(name string, source map[string]interface{}) map[string]interface{} {
	vol := map[string]interface{}{"name": name}
	for k, v := range source {
		vol[k] = v // Add keys like "emptyDir", "secret", "configMap" directly
	}
	return vol
}

func emptyDirVolumeSourceMap() map[string]interface{} {
	return map[string]interface{}{"emptyDir": map[string]interface{}{}} // Simplest EmptyDir
}

func secretVolumeSourceMap(secretName string) map[string]interface{} {
	return map[string]interface{}{"secret": map[string]interface{}{"secretName": secretName}}
}

func csiVolumeSourceMap(driver string, readOnly *bool, attributes map[string]string) map[string]interface{} {
	csiDetails := map[string]interface{}{
		"driver": driver,
	}
	if readOnly != nil {
		csiDetails["readOnly"] = *readOnly // Store the bool value
	}
	if attributes != nil {
		// The 'volumeAttributes' in CSIVolumeSource is map[string]string,
		// but when constructing unstructured, values are interface{}
		attrMap := make(map[string]interface{}, len(attributes))
		for k, v := range attributes {
			attrMap[k] = v
		}
		csiDetails["volumeAttributes"] = attrMap
	}
	return map[string]interface{}{"csi": csiDetails}
}

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool {
	return &b
}
