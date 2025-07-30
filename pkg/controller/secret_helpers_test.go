package controller

import (
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGetSecretToken(t *testing.T) {
	logger := testLogger()
	validToken := "my-secret-token-123"

	testCases := []struct {
		name          string
		inputObj      runtime.Object
		expectedToken string
	}{
		{
			name:          "valid secret with hf_token",
			inputObj:      newUnstructuredSecret(t, "test-secret-1", &validToken),
			expectedToken: validToken,
		},
		{
			name:          "secret with no data field",
			inputObj:      newUnstructuredSecret(t, "test-secret-2", nil), // Helper creates it with no data field
			expectedToken: "",
		},
		{
			name: "secret with empty data field",
			inputObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "Secret",
					"data": map[string]interface{}{},
				},
			},
			expectedToken: "",
		},
		{
			name: "secret data field is not a map",
			inputObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "Secret",
					"data": "this-is-not-a-map",
				},
			},
			expectedToken: "",
		},
		{
			name: "hf_token key is missing from data",
			inputObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "Secret",
					"data": map[string]interface{}{
						"other_key": "other_value",
					},
				},
			},
			expectedToken: "",
		},
		{
			name: "hf_token value is not a string",
			inputObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "Secret",
					"data": map[string]interface{}{
						"hf_token": 12345, // Not a string
					},
				},
			},
			expectedToken: "",
		},
		{
			name: "hf_token is not valid base64",
			inputObj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"kind": "Secret",
					"data": map[string]interface{}{
						"hf_token": "this-is-not-base64-$$$",
					},
				},
			},
			expectedToken: "",
		},
		{
			name:          "input object is not unstructured.Unstructured",
			inputObj:      &corev1.Secret{}, // Pass a typed object instead
			expectedToken: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotToken := getSecretToken(tc.inputObj, logger)
			if gotToken != tc.expectedToken {
				t.Errorf("getSecretToken() got = '%s', want '%s'", gotToken, tc.expectedToken)
			}
		})
	}
}

func TestSecretDiff(t *testing.T) {
	logger := testLogger()
	r := &GenericReconciler{}
	token1 := "my-secret-token-123"
	token2 := "a-different-token-456"

	testCases := []struct {
		name        string
		existingSec *unstructured.Unstructured
		desiredSec  *unstructured.Unstructured
		expectDiff  bool
	}{
		{
			name:        "identical secrets",
			existingSec: newUnstructuredSecret(t, "test-secret", &token1),
			desiredSec:  newUnstructuredSecret(t, "test-secret", &token1),
			expectDiff:  false,
		},
		{
			name:        "different secret tokens",
			existingSec: newUnstructuredSecret(t, "test-secret", &token1),
			desiredSec:  newUnstructuredSecret(t, "test-secret", &token2),
			expectDiff:  true,
		},
		{
			name:        "one has token, the other does not",
			existingSec: newUnstructuredSecret(t, "test-secret", &token1),
			desiredSec:  newUnstructuredSecret(t, "test-secret", nil),
			expectDiff:  true,
		},
		{
			name:        "both have no token",
			existingSec: newUnstructuredSecret(t, "test-secret", nil),
			desiredSec:  newUnstructuredSecret(t, "test-secret", nil),
			expectDiff:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			diff, err := r.secretDiff(tc.existingSec, tc.desiredSec, logger)
			if err != nil {
				t.Fatalf("secretDiff returned an unexpected error: %v", err)
			}
			if diff != tc.expectDiff {
				t.Errorf("secretDiff() = %v, want %v", diff, tc.expectDiff)
			}
		})
	}
}

// Helper function to create an unstructured Secret for testing
// It takes a plain token string and handles the base64 encoding for you.
func newUnstructuredSecret(t *testing.T, name string, token *string) *unstructured.Unstructured {
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "default",
			},
		},
	}

	if token != nil {
		encodedToken := base64.StdEncoding.EncodeToString([]byte(*token))
		secret.Object["data"] = map[string]interface{}{
			"hf_token": encodedToken,
		}
	}

	return secret
}
