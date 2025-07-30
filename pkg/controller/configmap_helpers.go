package controller

import (
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func (r *GenericReconciler) configMapDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {
	existingData := getConfigMapData(existingObj, log)
	desiredData := getConfigMapData(obj, log)
	return !reflect.DeepEqual(existingData, desiredData), nil
}

func getConfigMapData(obj runtime.Object, log logr.Logger) map[string]string {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		log.Info("Failed to convert runtime.Object to unstructured.Unstructured for ConfigMap")
		return nil
	}
	data, ok := unstructuredObj.Object["data"].(map[string]interface{})
	if !ok {
		log.Info("ConfigMap data not found or not a map")
		return nil
	}
	stringData := make(map[string]string)
	for k, v := range data {
		if strVal, ok := v.(string); ok {
			stringData[k] = strVal
		} else {
			log.Info("ConfigMap data value is not a string", "key", k, "value", v)
		}
	}
	return stringData
}
