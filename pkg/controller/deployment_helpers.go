package controller

import (
	"fmt"
	"reflect"
	"sort" // You added this for sorting ports
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (r *GenericReconciler) deploymentDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
	// Check for PodSpec diff
	// Processing limit as string
	// Processing request as string
	// getPodSpec - Found nodeSelector value as string
	// getPodSpec - Successfully extracted containerPort as int64
	existingPodSpec, err := getPodSpec(existingObj, log)
	if err != nil {
		return false, fmt.Errorf("error getting existing pod spec: %w", err)
	}
	// Processing limit as int
	// Processing request as int
	// Found nodeSelector value as string
	// getPodSpec - Successfully extracted containerPort as int
	newPodSpec, err := getPodSpec(obj, log)
	if err != nil {
		return false, fmt.Errorf("error getting new pod spec: %w", err)
	}
	cleanedExistingPodSpec := cleanPodSpec(existingPodSpec)
	cleanedNewPodSpec := cleanPodSpec(newPodSpec)

	//return !reflect.DeepEqual(cleanedNewPodSpec, cleanedExistingPodSpec), nil

	if !reflect.DeepEqual(cleanedExistingPodSpec, cleanedNewPodSpec) {
		diff := cmp.Diff(cleanedExistingPodSpec, cleanedNewPodSpec)
		log.Info("Found a difference in the PodSpec for Deployment", "difference", diff)
		return true, nil
	}

	return false, nil
}

func getPodSpec(obj *unstructured.Unstructured, log logr.Logger) (*corev1.PodSpec, error) {
	if obj == nil {
		return nil, fmt.Errorf("input object is nil")
	}
	spec, err := getNestedMap(obj.Object, "spec")
	if err != nil {
		return nil, fmt.Errorf("error getting spec: %w", err)
	}

	template, err := getNestedMap(spec, "template")
	if err != nil {
		return nil, fmt.Errorf("error getting template: %w", err)
	}

	podSpecMap, err := getNestedMap(template, "spec")
	if err != nil {
		return nil, fmt.Errorf("error getting pod spec: %w", err)
	}

	containersMapListFromSpec := getSliceOfMaps(podSpecMap, "containers", log)

	podSpec := &corev1.PodSpec{
		ServiceAccountName: getStringValue(podSpecMap, "serviceAccountName"),
		Volumes:            getVolumes(podSpecMap, log),
		Containers:         getContainers(containersMapListFromSpec, log), // Use the fetched list
		InitContainers:     getContainers(getSliceOfMaps(podSpecMap, "initContainers", log), log),
		RestartPolicy:      corev1.RestartPolicy(getStringValue(podSpecMap, "restartPolicy")),
		Tolerations:        getTolerations(podSpecMap, log),
		SecurityContext:    getPodSecurityContext(podSpecMap, log),
		RuntimeClassName:   getStringPtrValue(podSpecMap, "runtimeClassName"),
	}

	return podSpec, nil
}

func getVolumes(podSpecMap map[string]interface{}, log logr.Logger) []corev1.Volume {
	var volumes []corev1.Volume
	if podSpecMap == nil {
		return volumes // Return empty non-nil slice
	}
	volumesVal, keyOk := podSpecMap["volumes"]
	if !keyOk {
		log.V(1).Info("getVolumes: 'volumes' key not found in podSpecMap")
		return volumes // Return empty non-nil slice
	}

	// Attempt 1: Is it already []map[string]interface{}?
	if specificList, ok := volumesVal.([]map[string]interface{}); ok {
		for _, volumeMap := range specificList { // Iterate directly over the correctly typed slice
			volume := corev1.Volume{
				Name:         getStringValue(volumeMap, "name"),
				VolumeSource: getVolumeSource(volumeMap, log), // Ensure getVolumeSource is robust
			}
			volumes = append(volumes, volume)
		}
	} else if genericList, ok := volumesVal.([]interface{}); ok {
		// Attempt 2: Is it []interface{} where each element is a map?
		for _, item := range genericList {
			if volumeMap, ok := item.(map[string]interface{}); ok {
				volume := corev1.Volume{
					Name:         getStringValue(volumeMap, "name"),
					VolumeSource: getVolumeSource(volumeMap, log),
				}
				volumes = append(volumes, volume)
			} else {
				log.Info("Skipping volume entry (item in generic list is not a map)", "value", item, "type", fmt.Sprintf("%T", item))
			}
		}
	} else {
		log.Info("Volumes field is not a recognized list type for maps", "value", volumesVal, "type", fmt.Sprintf("%T", volumesVal))
	}

	return volumes
}

// getContainers now accepts a slice of maps directly,
// and iterates over them to build a slice of corev1.Container.
func getContainers(containersMapList []map[string]interface{}, log logr.Logger) []corev1.Container {
	var containers []corev1.Container
	for _, containerMap := range containersMapList { // Iterate over the provided list of maps
		container := corev1.Container{
			Name:           getStringValue(containerMap, "name"),
			Image:          getStringValue(containerMap, "image"),
			Command:        getStringList(containerMap, "command"),
			Args:           getStringList(containerMap, "args"),
			Resources:      getResources(containerMap, log),
			Env:            getEnvVars(containerMap),
			VolumeMounts:   getVolumeMounts(containerMap),
			Ports:          getContainerPorts(containerMap, log),
			ReadinessProbe: getReadinessProbe(containerMap, log), // This is probably not relevant for init/containers, but keep if needed
			// Add other Container fields as needed (e.g., workingDir, securityContext)
		}
		containers = append(containers, container)
	}
	return containers
}

// getTolerations safely parses the tolerations slice.
func getTolerations(podSpecMap map[string]interface{}, log logr.Logger) []corev1.Toleration {
	var tolerations []corev1.Toleration
	if tolerationsVal, ok := podSpecMap["tolerations"]; ok {
		if tolerationList, ok := tolerationsVal.([]interface{}); ok {
			for _, item := range tolerationList {
				if tolMap, ok := item.(map[string]interface{}); ok {
					toleration := corev1.Toleration{
						Key:      getStringValue(tolMap, "key"),
						Operator: corev1.TolerationOperator(getStringValue(tolMap, "operator")),
						Value:    getStringValue(tolMap, "value"),
						Effect:   corev1.TaintEffect(getStringValue(tolMap, "effect")),
					}
					if tolSecs, found, _ := unstructured.NestedInt64(tolMap, "tolerationSeconds"); found {
						toleration.TolerationSeconds = &tolSecs
					}
					tolerations = append(tolerations, toleration)
				}
			}
		}
	}
	return tolerations
}

// getPodSecurityContext safely parses the pod-level security context.
func getPodSecurityContext(podSpecMap map[string]interface{}, log logr.Logger) *corev1.PodSecurityContext {
	if pscVal, ok := podSpecMap["securityContext"]; ok {
		if pscMap, ok := pscVal.(map[string]interface{}); ok {
			psc := &corev1.PodSecurityContext{}
			if runAsUser, found, _ := unstructured.NestedInt64(pscMap, "runAsUser"); found {
				psc.RunAsUser = &runAsUser
			}
			if runAsNonRoot, found, _ := unstructured.NestedBool(pscMap, "runAsNonRoot"); found {
				psc.RunAsNonRoot = &runAsNonRoot
			}
			if fsGroup, found, _ := unstructured.NestedInt64(pscMap, "fsGroup"); found {
				psc.FSGroup = &fsGroup
			}
			if seccomp, found, _ := unstructured.NestedMap(pscMap, "seccompProfile"); found {
				psc.SeccompProfile = &corev1.SeccompProfile{
					Type: corev1.SeccompProfileType(getStringValue(seccomp, "type")),
				}
			}
			return psc
		}
	}
	return nil
}

// getStringPtrValue returns a pointer to a string, or nil if not found.
func getStringPtrValue(data map[string]interface{}, key string) *string {
	if val, ok := data[key].(string); ok && val != "" {
		return &val
	}
	return nil
}

func getReadinessProbe(containerMap map[string]interface{}, log logr.Logger) *corev1.Probe {
	if readinessProbeInterface, ok := containerMap["readinessProbe"]; ok {
		readinessProbeMap, ok := readinessProbeInterface.(map[string]interface{})
		if ok {
			newReadinessProbe := &corev1.Probe{}
			if httpGetInterface, ok := readinessProbeMap["httpGet"]; ok {
				httpGetMap, ok := httpGetInterface.(map[string]interface{})
				if ok {
					newHTTPGetAction := &corev1.HTTPGetAction{}
					if portInterface, ok := httpGetMap["port"]; ok {
						portMap, ok := portInterface.(map[string]interface{})
						if ok {
							if intVal, ok := portMap["intVal"].(int64); ok {
								newHTTPGetAction.Port = intstr.FromInt(int(intVal))
							}
						}
					}
					if path, ok := httpGetMap["path"].(string); ok {
						newHTTPGetAction.Path = path
					}
					newReadinessProbe.ProbeHandler.HTTPGet = newHTTPGetAction
				}

			}
			return newReadinessProbe
		}
	}
	return nil
}

func getResources(containerMap map[string]interface{}, log logr.Logger) corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{} // Initialize an empty struct

	// Ensure Limits and Requests are always non-nil maps
	resources.Limits = corev1.ResourceList{}
	resources.Requests = corev1.ResourceList{}
	// Claims are slices, which your current code likely handles as nil vs empty.
	// Ensure Claims is an empty slice rather than nil if no claims.
	resources.Claims = []corev1.ResourceClaim{} // Ensure Claims is an empty slice, not nil

	if resourcesInterface, ok := containerMap["resources"]; ok {
		if resourcesMap, ok := resourcesInterface.(map[string]interface{}); ok {
			// These calls will populate the already non-nil maps
			resources.Limits = getResourceList(resourcesMap, "limits", log)
			resources.Requests = getResourceList(resourcesMap, "requests", log)
		}
	}
	return resources
}

func getResourceList(resourcesMap map[string]interface{}, key string, log logr.Logger) corev1.ResourceList {
	resourceList := corev1.ResourceList{}
	if values, ok := resourcesMap[key].(map[string]interface{}); ok {
		for k, v := range values {
			var quantity resource.Quantity
			var err error

			switch val := v.(type) {
			case string:
				quantity, err = resource.ParseQuantity(val)
			case int64:
				quantity = *resource.NewQuantity(val, resource.BinarySI) // Assume BinarySI for memory, adjust for CPU
			case int:
				quantity = *resource.NewQuantity(int64(val), resource.BinarySI)
			case int32:
				quantity = *resource.NewQuantity(int64(val), resource.BinarySI)
			case float64: // This is tricky, floats for quantities can be problematic
				// Try to convert to int64, but beware of precision loss
				quantity = *resource.NewQuantity(int64(val), resource.BinarySI)
			default:
				log.Info("Unknown type for resource quantity value, skipping", "key", k, "value", v, "type", fmt.Sprintf("%T", v))
				continue
			}

			if err != nil {
				log.Info("Error parsing quantity, skipping", "key", k, "value", v, "error", err)
				continue
			}
			// Canonicalize the quantity before adding to the list
			resourceList[corev1.ResourceName(k)] = canonicalizeResourceQuantity(corev1.ResourceName(k), quantity)
		}
	}
	return resourceList
}

// Helper to canonicalize resource.Quantity for reliable DeepEqual comparison
// This ensures that "1Gi" and "1073741824" (bytes) compare as equal
func canonicalizeResourceQuantity(resourceName corev1.ResourceName, q resource.Quantity) resource.Quantity {
	switch resourceName {
	case corev1.ResourceCPU:
		// Always canonicalize CPU to millicores (DecimalSI)
		return *resource.NewMilliQuantity(q.MilliValue(), resource.DecimalSI)
	case corev1.ResourceMemory, "nvidia.com/gpu": // Assuming GPU quantities are also treated like memory or other binary units for simplicity if not millicores
		// Always canonicalize memory/binary units to bytes (BinarySI)
		return *resource.NewQuantity(q.Value(), resource.BinarySI)
	default:
		// For unknown resources, keep the original format or choose a default
		return q // Or consider canonicalizing to Value() and a default format like BinarySI
	}
}

func getEnvVarSource(valueFromMap map[string]interface{}) *corev1.EnvVarSource {
	envVarSource := &corev1.EnvVarSource{}
	if secretKeyRef, ok := valueFromMap["secretKeyRef"].(map[string]interface{}); ok {
		envVarSource.SecretKeyRef = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: getStringValue(secretKeyRef, "name"),
			},
			Key: getStringValue(secretKeyRef, "key"),
			// Optional: add Optional: getBoolPtrValue(secretKeyRef, "optional") if you need to compare it
		}
	}
	// Add other EnvVarSource types as needed (configMapKeyRef, fieldRef, resourceFieldRef)
	// Make sure to handle all possible types your templates might use to avoid missing diffs.
	return envVarSource
}

func getEnvVars(containerMap map[string]interface{}) []corev1.EnvVar {
	var envVars []corev1.EnvVar
	if envInterface, ok := containerMap["env"]; ok {
		if envList, ok := envInterface.([]interface{}); ok {
			for _, v := range envList {
				if envVarMap, ok := v.(map[string]interface{}); ok {
					newEnvVar := corev1.EnvVar{
						Name:  getStringValue(envVarMap, "name"),
						Value: getStringValue(envVarMap, "value"),
					}
					if valueFrom, ok := envVarMap["valueFrom"].(map[string]interface{}); ok {
						newEnvVar.ValueFrom = getEnvVarSource(valueFrom)
					}
					envVars = append(envVars, newEnvVar)
				}
			}
		}
	}
	sort.Slice(envVars, func(i, j int) bool {
		return envVars[i].Name < envVars[j].Name
	})

	return envVars
}

func getVolumeMounts(containerMap map[string]interface{}) []corev1.VolumeMount {
	var volumeMounts []corev1.VolumeMount
	if volumeMountsInterface, ok := containerMap["volumeMounts"]; ok {
		if volumeMountsList, ok := volumeMountsInterface.([]interface{}); ok {
			for _, v := range volumeMountsList {
				if volumeMountMap, ok := v.(map[string]interface{}); ok {
					newVolumeMount := corev1.VolumeMount{
						Name:      getStringValue(volumeMountMap, "name"),
						MountPath: getStringValue(volumeMountMap, "mountPath"),
						ReadOnly:  getBoolValue(volumeMountMap, "readOnly"),
						// Add other fields you care about (e.g., SubPath)
					}
					volumeMounts = append(volumeMounts, newVolumeMount)
				}
			}
		}
	}
	sort.Slice(volumeMounts, func(i, j int) bool {
		if volumeMounts[i].Name != volumeMounts[j].Name {
			return volumeMounts[i].Name < volumeMounts[j].Name
		}
		return volumeMounts[i].MountPath < volumeMounts[j].MountPath // Secondary sort key
	})
	return volumeMounts
}

func getContainerPorts(containerMap map[string]interface{}, log logr.Logger) []corev1.ContainerPort {
	var ports []corev1.ContainerPort
	if portsInterface, ok := containerMap["ports"]; ok {
		if portsList, ok := portsInterface.([]interface{}); ok {
			for _, v := range portsList {
				if portMap, ok := v.(map[string]interface{}); ok {

					// Get the protocol from the map
					protocol := corev1.Protocol(getStringValue(portMap, "protocol"))

					// If the protocol is empty, set the default to TCP
					if protocol == "" {
						protocol = corev1.ProtocolTCP
					}

					newPort := corev1.ContainerPort{
						Name:          getStringValue(portMap, "name"),
						Protocol:      protocol, // Use the (possibly now defaulted) protocol
						ContainerPort: getInt32Value(portMap, "containerPort", log),
					}
					ports = append(ports, newPort)
				}
			}
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		return ports[i].ContainerPort < ports[j].ContainerPort
	})
	return ports
}

func cleanPodSpec(input *corev1.PodSpec) *corev1.PodSpec {
	containers := []corev1.Container{}
	for _, inputContainer := range input.Containers {
		container := &corev1.Container{
			Name:           inputContainer.Name,
			Image:          inputContainer.Image,
			Command:        inputContainer.Command,
			Args:           inputContainer.Args,
			Resources:      inputContainer.Resources,
			Env:            inputContainer.Env,
			VolumeMounts:   inputContainer.VolumeMounts,
			Ports:          inputContainer.Ports,
			ReadinessProbe: cleanProbe(inputContainer.ReadinessProbe),
		}
		containers = append(containers, *container)
	}

	sort.Slice(containers, func(i, j int) bool {
		return containers[i].Name < containers[j].Name
	})

	if input.Volumes != nil {
		sort.Slice(input.Volumes, func(i, j int) bool {
			return input.Volumes[i].Name < input.Volumes[j].Name
		})
	}

	return &corev1.PodSpec{
		Containers:         containers,
		Volumes:            input.Volumes,
		NodeSelector:       input.NodeSelector,
		ServiceAccountName: input.ServiceAccountName,
		Tolerations:        cleanTolerations(input.Tolerations),
		SecurityContext:    cleanSecurityContext(input.SecurityContext),
		RuntimeClassName:   input.RuntimeClassName,
	}
}

func cleanProbe(p *corev1.Probe) *corev1.Probe {
	if p == nil {
		return nil
	}
	clean := &corev1.Probe{
		InitialDelaySeconds: p.InitialDelaySeconds,
		PeriodSeconds:       p.PeriodSeconds,
	}
	if p.HTTPGet != nil {
		clean.HTTPGet = &corev1.HTTPGetAction{
			Path: p.HTTPGet.Path,
			Port: p.HTTPGet.Port,
		}
	}
	// Add other probe types like TCPSocket or Exec if you use them
	return clean
}

// cleanSecurityContext creates a new PodSecurityContext containing only user-controlled fields.
func cleanSecurityContext(psc *corev1.PodSecurityContext) *corev1.PodSecurityContext {
	if psc == nil {
		return nil
	}
	clean := &corev1.PodSecurityContext{
		// Only compare fields that are not mutated by Autopilot.
		RunAsNonRoot: psc.RunAsNonRoot,
	}
	if psc.SeccompProfile != nil {
		clean.SeccompProfile = &corev1.SeccompProfile{
			Type: psc.SeccompProfile.Type,
		}
	}
	return clean
}

// cleanTolerations removes default tolerations added by Kubernetes.
func cleanTolerations(tolerations []corev1.Toleration) []corev1.Toleration {
	var cleaned []corev1.Toleration
	for _, t := range tolerations {
		// This is an example. You would add checks to exclude any tolerations
		// that are automatically added by the system and not defined in your templates.
		// For now, we are focusing on the gVisor one which is user-defined.
		if strings.HasPrefix(t.Key, "sandbox.gke.io/") {
			cleaned = append(cleaned, t)
		}
	}
	// Sort for stable comparison
	sort.Slice(cleaned, func(i, j int) bool {
		return cleaned[i].Key < cleaned[j].Key
	})
	return cleaned
}

func getVolumeSource(volumeItemMap map[string]interface{}, log logr.Logger) corev1.VolumeSource {
	vs := corev1.VolumeSource{} // Start with an empty one

	// Check for each known volume type directly on volumeItemMap
	if emptyDirData, ok := volumeItemMap["emptyDir"]; ok {
		emptyDirActualMap, isMap := emptyDirData.(map[string]interface{})
		// Handles "emptyDir: {}" (where emptyDirData is map[string]interface{}{})
		// or "emptyDir: null" (where emptyDirData is nil)
		if emptyDirData == nil || isMap {
			if emptyDirActualMap == nil { // Ensure map is not nil for getEmptyDirVolumeSource
				emptyDirActualMap = make(map[string]interface{})
			}
			vs.EmptyDir = getEmptyDirVolumeSource(emptyDirActualMap)
		} else {
			log.Info("Volume 'emptyDir' value is not a map nor nil", "data", emptyDirData)
		}
	} else if secretData, ok := volumeItemMap["secret"]; ok {
		if secretMap, ok := secretData.(map[string]interface{}); ok {
			secretVolumeSource := &corev1.SecretVolumeSource{} // Initialize the struct
			secretVolumeSource.SecretName = getStringValue(secretMap, "secretName")
			if optionalVal, found := secretMap["optional"]; found {
				if optBool, isBool := optionalVal.(bool); isBool {
					secretVolumeSource.Optional = &optBool
				}
			}
			// TODO: Parse 'items' if your diff logic cares about them:
			// secretVolumeSource.Items = getVolumeItems(secretMap, "items")
			vs.Secret = secretVolumeSource
		} else {
			log.Info("Volume 'secret' value is not a map", "data", secretData)
		}
	} else if configMapData, ok := volumeItemMap["configMap"]; ok {
		if configMapMap, ok := configMapData.(map[string]interface{}); ok {
			configMapVolumeSource := &corev1.ConfigMapVolumeSource{}          // Initialize the struct
			configMapVolumeSource.Name = getStringValue(configMapMap, "name") // Name of the ConfigMap
			if optionalVal, found := configMapMap["optional"]; found {
				if optBool, isBool := optionalVal.(bool); isBool {
					configMapVolumeSource.Optional = &optBool
				}
			}
			// TODO: Parse 'items'
			vs.ConfigMap = configMapVolumeSource
		} else {
			log.Info("Volume 'configMap' value is not a map", "data", configMapData)
		}
	} else if csiData, ok := volumeItemMap["csi"]; ok {
		if csiMap, ok := csiData.(map[string]interface{}); ok {
			vs.CSI = getCSIVolumeSource(csiMap, log) // Ensure this returns non-nil *corev1.CSIVolumeSource
		} else {
			log.Info("Volume 'csi' value is not a map", "data", csiData)
		}
	}
	// Add more 'else if' blocks here for other volume types you support
	// (e.g., HostPath, PersistentVolumeClaim)

	return vs
}
func getEmptyDirVolumeSource(emptyDir map[string]interface{}) *corev1.EmptyDirVolumeSource {
	emptyDirVolumeSource := &corev1.EmptyDirVolumeSource{}
	if medium, ok := emptyDir["medium"].(string); ok {
		emptyDirVolumeSource.Medium = corev1.StorageMedium(medium)
	}
	return emptyDirVolumeSource
}
func getCSIVolumeSource(csi map[string]interface{}, log logr.Logger) *corev1.CSIVolumeSource {
	csiVolumeSource := &corev1.CSIVolumeSource{}

	if driver, ok := csi["driver"].(string); ok {
		csiVolumeSource.Driver = driver
	}
	if readOnly, ok := csi["readOnly"].(bool); ok {
		csiVolumeSource.ReadOnly = &readOnly
	}
	if volumeAttributes, ok := csi["volumeAttributes"].(map[string]interface{}); ok {
		csiVolumeSource.VolumeAttributes = make(map[string]string)
		for k, v := range volumeAttributes {
			if s, ok := v.(string); ok {
				csiVolumeSource.VolumeAttributes[k] = s
			}
		}
	}
	return csiVolumeSource
}

func getBoolValue(data map[string]interface{}, key string) bool {
	if data == nil {
		return false
	}
	if value, ok := data[key]; ok {
		if b, ok := value.(bool); ok {
			return b
		}
		// Consider if "true" or "false" strings should also be parsed
		if s, ok := value.(string); ok {
			return strings.ToLower(s) == "true"
		}
	}
	return false
}
