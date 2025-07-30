package controller

import (
	"encoding/base64"
	"reflect"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func (r *GenericReconciler) secretDiff(existingObj, obj *unstructured.Unstructured, log logr.Logger) (bool, error) {

	existingToken := getSecretToken(existingObj, log)
	desiredToken := getSecretToken(obj, log)
	return !reflect.DeepEqual(existingToken, desiredToken), nil
}

func getSecretToken(obj runtime.Object, log logr.Logger) string {
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		log.Info("Failed to convert runtime.Object to unstructured.Unstructured for Secret")
		return ""
	}

	data, ok := unstructuredObj.Object["data"].(map[string]interface{})
	if !ok {
		log.Info("Secret data not found or not a map")
		return ""
	}

	encodedTokenInterface, ok := data["hf_token"]
	if !ok {
		log.Info("HF Token not found")
		return ""
	}
	encodedToken, ok := encodedTokenInterface.(string)
	if !ok {
		return ""
	}

	decodedToken, err := base64.StdEncoding.DecodeString(string(encodedToken))
	if err != nil {
		log.Error(err, "Failed to decode base64 string")
		return ""
	}

	return string(decodedToken)
}
