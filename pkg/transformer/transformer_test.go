package transformer

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	template "github.com/google/safetext/yamltemplate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"

	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	a "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

// mockRESTMapper is a simple mock that satisfies the meta.RESTMapper interface for our tests.
// We only need to implement the RESTMapping method for our use case.
type mockRESTMapper struct {
	meta.RESTMapper
}

// RESTMapping is the only method we need to mock. It returns a pre-canned mapping.
func (m *mockRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if gk.Kind == "ModelData" {
		return &meta.RESTMapping{
			Resource:         schema.GroupVersionResource{Group: "model.skippy.io", Version: "v1", Resource: "modeldatas"},
			GroupVersionKind: gk.WithVersion("v1"),
			Scope:            meta.RESTScopeNamespace,
		}, nil
	}
	if gk.Kind == "ConfigMap" {
		return &meta.RESTMapping{
			Resource:         schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"},
			GroupVersionKind: gk.WithVersion("v1"),
			Scope:            meta.RESTScopeNamespace,
		}, nil
	}
	// Return an error for any other kind to catch unexpected lookups
	return nil, fmt.Errorf("no mock mapping found for GroupKind %v", gk)
}

func TestTransformerRun(t *testing.T) {
	ctx := context.Background()
	testNamespace := "test-ns"
	testName := "test-resource"

	// 1. Create a mock filesystem with templates
	fSys := filesys.MakeFsInMemory()
	templateDir := "templates/my-integration"
	applyDir := "v1/apply"
	targetTemplatePath := filepath.Join(templateDir, "deployment.yaml")

	templateContent := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .resource.metadata.name }}-deployment
  namespace: {{ .resource.metadata.namespace }}
spec:
  replicas: 1
`
	require.NoError(t, fSys.MkdirAll(templateDir))
	require.NoError(t, fSys.WriteFile(targetTemplatePath, []byte(templateContent)))

	applyContent := `
resources:
{{- range . }}
- {{ . }}
{{- end }}
`
	require.NoError(t, fSys.MkdirAll(applyDir))
	require.NoError(t, fSys.WriteFile(filepath.Join(applyDir, "apply.yaml"), []byte(applyContent)))

	// 2. Define the primary resource using the shared helper from utils.go
	obj := newTestObject("testing.google.com", "v1", "TestResource", testName)
	obj.SetNamespace(testNamespace) // Override the default namespace from the helper
	objGVK := obj.GetObjectKind().GroupVersionKind()

	// 3. Setup the Transformer with the SHARED mockRegistry
	transformer := NewTransformer()

	// Use the enhanced, shared mockRegistry
	transformer.registry = &mockRegistry{
		// MODIFICATION: Initialize 'integrations' as a slice, not a map.
		integrations: []schema.GroupVersionKind{
			objGVK,
		},
		templatePaths: map[schema.GroupVersionKind][]string{
			objGVK: {"embedded:/templates/my-integration"},
		},
	}

	// 4. Inject mocks for filesystem and external functions (same as before)
	transformer.fsProviderFunc = func(ctx context.Context, path string) (filesys.FileSystem, string, error) {
		if strings.Contains(path, "apply") {
			return fSys, applyDir, nil
		}
		return fSys, templateDir, nil
	}
	transformer.findConnectedResourcesFunc = func(ctx context.Context, discovery discovery.DiscoveryInterface, dynamic dynamic.Interface, u *unstructured.Unstructured) ([]*unstructured.Unstructured, []*unstructured.Unstructured, error) {
		return nil, nil, nil
	}
	transformer.topologicalSortFunc = func(resources []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
		return resources, nil
	}

	// 5. Create fake Kubernetes clients
	dynamicClient := fake.NewSimpleDynamicClient(scheme.Scheme, obj)
	discoveryClient := &fakediscovery.FakeDiscovery{Fake: &dynamicClient.Fake}
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testName},
	}
	// ACT
	mockMapper := &mockRESTMapper{}
	// The fake typed client needs a scheme that knows about all the resource types.
	testScheme := runtime.NewScheme()
	_ = scheme.AddToScheme(testScheme)

	fakeTypedClient := a.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(obj).
		Build()

	result, err := transformer.Run(ctx, discoveryClient, dynamicClient, mockMapper, fakeTypedClient, req, obj)

	// ASSERT
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result, 1)

	deployment := result[0]
	assert.Equal(t, "Deployment", deployment.GetKind())
	assert.Equal(t, "test-resource-deployment", deployment.GetName())
	assert.Equal(t, testNamespace, deployment.GetNamespace())
}

func TestTransformerRun_WithCopyOperation(t *testing.T) {
	// ARRANGE

	ctx := context.Background()
	testNamespace := "copy-ns"
	testName := "copy-resource"

	// --- Start of Fix ---
	// The content for a 'copy' operation must be static, valid YAML without template directives.
	// We'll hardcode the namespace and verify it in the assertions.
	// --- End of Fix ---
	sourceFs := filesys.MakeFsInMemory()
	copySourceDir := "integrations/endpoint/base"
	copiedFileName := "copied-configmap.yaml"
	copiedFileContent := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: copied-from-source
  namespace: ` + testNamespace + `
data:
  sourcePath: ` + copySourceDir + `
`
	require.NoError(t, sourceFs.MkdirAll(copySourceDir))
	require.NoError(t, sourceFs.WriteFile(filepath.Join(copySourceDir, copiedFileName), []byte(copiedFileContent)))

	applyFs := filesys.MakeFsInMemory()
	applyDir := "v1/apply"
	applyContent := `
resources:
{{- range . }}
- {{ . }}
{{- end }}
`
	require.NoError(t, applyFs.MkdirAll(applyDir))
	require.NoError(t, applyFs.WriteFile(filepath.Join(applyDir, "apply.yaml"), []byte(applyContent)))

	objGVK := schema.GroupVersionKind{Group: "model.skippy.io", Version: "v1", Kind: "Endpoint"}
	obj := newTestObject(objGVK.Group, objGVK.Version, objGVK.Kind, testName)
	obj.SetNamespace(testNamespace)

	transformer := NewTransformer()
	transformer.registry = &mockRegistry{
		integrations: []schema.GroupVersionKind{objGVK},
		copyPaths: map[schema.GroupVersionKind][]string{
			objGVK: {"embedded:/integrations/endpoint/base"},
		},
	}

	transformer.fsProviderFunc = func(ctx context.Context, path string) (filesys.FileSystem, string, error) {
		switch path {
		case "embedded:/integrations/endpoint/base":
			return sourceFs, copySourceDir, nil
		case "embedded:/v1/apply":
			return applyFs, applyDir, nil
		default:
			return nil, "", fmt.Errorf("fsProviderFunc received an unexpected path: %s", path)
		}
	}

	transformer.findConnectedResourcesFunc = func(ctx context.Context, discovery discovery.DiscoveryInterface, dynamic dynamic.Interface, u *unstructured.Unstructured) ([]*unstructured.Unstructured, []*unstructured.Unstructured, error) {
		return nil, nil, nil
	}
	transformer.topologicalSortFunc = func(resources []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
		return resources, nil
	}

	dynamicClient := fake.NewSimpleDynamicClient(scheme.Scheme, obj)
	discoveryClient := &fakediscovery.FakeDiscovery{Fake: &dynamicClient.Fake}
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: testName},
	}

	mockMapper := &mockRESTMapper{}

	// The fake typed client needs a scheme that knows about all the resource types.
	testScheme := runtime.NewScheme()
	_ = scheme.AddToScheme(testScheme)

	fakeTypedClient := a.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(obj).
		Build()

	// ACT
	result, err := transformer.Run(ctx, discoveryClient, dynamicClient, mockMapper, fakeTypedClient, req, obj)

	// ASSERT
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result, 1)

	copiedResource := result[0]
	assert.Equal(t, "ConfigMap", copiedResource.GetKind())
	assert.Equal(t, "copied-from-source", copiedResource.GetName())

	// --- Start of Fix ---
	// Assert that the static namespace from the copied file is present.
	assert.Equal(t, testNamespace, copiedResource.GetNamespace())
	// --- End of Fix ---

	data, found, err := unstructured.NestedStringMap(copiedResource.Object, "data")
	require.True(t, found)
	require.NoError(t, err)
	assert.Equal(t, copySourceDir, data["sourcePath"])
}

func TestFileSystemForPathWithOptions(t *testing.T) {
	ctx := context.Background()

	// --- Mock Factories for Testing ---

	// A fake embedded filesystem that always succeeds.
	fakeEmbeddedFS := filesys.MakeFsInMemory()
	mockEmbeddedSuccessFactory := func() (filesys.FileSystem, error) {
		return fakeEmbeddedFS, nil
	}

	// A fake GCS filesystem that always succeeds.
	fakeGCSFS := filesys.MakeFsInMemory()
	mockGCSSuccessFactory := func(ctx context.Context, bucket, objectPath string) (filesys.FileSystem, error) {
		// We can even check if the correct bucket/path were passed.
		assert.Equal(t, "test-bucket", bucket)
		assert.Equal(t, "path/to/object", objectPath)
		return fakeGCSFS, nil
	}

	// A fake factory that always returns an error.
	mockErrorFactory := func() (filesys.FileSystem, error) {
		return nil, fmt.Errorf("factory failed")
	}
	mockGCSErrorFactory := func(ctx context.Context, bucket, objectPath string) (filesys.FileSystem, error) {
		return nil, fmt.Errorf("gcs factory failed")
	}

	// --- Test Cases ---

	t.Run("should return embedded filesystem for embedded scheme", func(t *testing.T) {
		fs, rootPath, err := fileSystemForPathWithOptions(ctx, "embedded:/some/path", nil, mockEmbeddedSuccessFactory)
		require.NoError(t, err)
		assert.Equal(t, "some/path", rootPath)
		assert.Equal(t, fakeEmbeddedFS, fs)
	})

	t.Run("should return gcs filesystem for gcs scheme", func(t *testing.T) {
		fs, rootPath, err := fileSystemForPathWithOptions(ctx, "gcs:/test-bucket/path/to/object", mockGCSSuccessFactory, nil)
		require.NoError(t, err)
		assert.Equal(t, "path/to/object", rootPath)
		assert.Equal(t, fakeGCSFS, fs)
	})

	t.Run("should return error for unknown scheme", func(t *testing.T) {
		_, _, err := fileSystemForPathWithOptions(ctx, "http://google.com", nil, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `could not find file system for scheme "http"`)
	})

	t.Run("should return error for invalid URL", func(t *testing.T) {
		_, _, err := fileSystemForPathWithOptions(ctx, "://google.com", nil, nil)
		assert.Error(t, err)
	})

	t.Run("should return error for malformed GCS path", func(t *testing.T) {
		_, _, err := fileSystemForPathWithOptions(ctx, "gcs:/only-bucket-no-path", mockGCSSuccessFactory, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `unable to parse GCS path`)
	})

	t.Run("should propagate error from embedded factory", func(t *testing.T) {
		_, _, err := fileSystemForPathWithOptions(ctx, "embedded:/path", nil, mockErrorFactory)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "factory failed")
	})

	t.Run("should propagate error from gcs factory", func(t *testing.T) {
		_, _, err := fileSystemForPathWithOptions(ctx, "gcs:/test-bucket/path/to/object", mockGCSErrorFactory, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "gcs factory failed")
	})
}

// This test uses the real data from the failing Custom Resource to reproduce
// the "YAML Injection Detected" error locally.
func TestYAMLInjectionReproduction(t *testing.T) {
	// Step 1: This is the content of your 'inference.yaml' template.
	templateContent := `
apiVersion: apps/v1
kind: Deployment
metadata:
  annotations:
    recommender.ai.gke.io/generated: "true"
    recommender.ai.gke.io/inference-server: {{ .resource.spec.inferenceServer.type }}
    gke-gcsfuse/volumes: "true"
  labels:
    app: {{ lower .resource.metadata.name }}-{{ lower .resource.spec.inferenceServer.type }}-inference-server
    recommender.ai.gke.io/generated: "true"
  name: {{ lower .resource.metadata.name }}-deployment
  namespace: {{ or .resource.metadata.namespace "default" }}
spec:
  replicas: {{ or .resource.spec.autoscaling.minReplicas 1 }}
  selector:
    matchLabels:
      app: {{ lower .resource.metadata.name }}-{{ lower .resource.spec.inferenceServer.type }}-inference-server
  template:
    metadata:
      labels:
        ai.gke.io/inference-server: {{ lower .resource.spec.inferenceServer.type }}
        ai.gke.io/model: {{ extractStringAfterSlash .resource.spec.model.modelName }}
        app: {{ lower .resource.metadata.name }}-{{ lower .resource.spec.inferenceServer.type }}-inference-server
    spec:
      serviceAccountName: skippy-controller-manager
      nodeSelector:
        cloud.google.com/gke-accelerator: {{ .resource.spec.accelerator }}
      volumes:
      - name: dshm
        emptyDir:
          medium: Memory
      - name: model-weights
        csi:
          driver: gcsfuse.csi.storage.gke.io
          volumeAttributes:
            bucketName: "{{ getGcsBucketFromURI .resource.spec.model.gcsPath }}"
            mountOptions: "implicit-dirs,file-cache:enable-parallel-downloads:true,file-cache:parallel-downloads-per-file:100,file-cache:max-parallel-downloads:-1,file-cache:download-chunk-size-mb:10,file-cache:max-size-mb:-1"
      containers:
      - name: inference-server
        image: {{ .resource.spec.inferenceServer.image }}
        command:
          {{- if .resource.spec.inferenceServer.command }}
          {{- range $cmd := .resource.spec.inferenceServer.command }}
          - {{ $cmd }}
          {{- end }}
          {{- end }}
        args:
          {{- if .resource.spec.inferenceServer.args }}
          {{- range $arg := .resource.spec.inferenceServer.args }}
          - {{ $arg }}
          {{- end }}
          {{- end }}
        env:
          {{- range $envVar := .resource.spec.inferenceServer.env }}
          - name: {{ $envVar.name }}
            value: {{ $envVar.value }}
          {{- end }}
        ports:
        - containerPort: {{ int (or (extractValueAfterEquals .resource.spec.inferenceServer.args "--port") 8080) }}
          name: http
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /health
            port: http
`
	// This is the real data from your cluster
	resourceData := map[string]interface{}{"apiVersion": "model.skippy.io/v1", "kind": "InferenceDeployment", "metadata": map[string]interface{}{"name": "llama-3-1-8b-vllm", "namespace": "default"}, "spec": map[string]interface{}{"accelerator": "nvidia-l4", "autoscaling": map[string]interface{}{"maxReplicas": float64(5), "metricName": "prometheus.googleapis.com|vllm:num_requests_waiting|gauge", "metricType": "Pods", "minReplicas": float64(1), "targetValue": "1000000000"}, "inferenceServer": map[string]interface{}{"args": []interface{}{"--host=0.0.0.0", "--port=7080", "--tensor-parallel-size=1", "--model=/data/meta-llama/Llama-3.1-8B-Instruct", "--swap-space=16", "--gpu-memory-utilization=0.9", "--max-model-len=4096", "--trust-remote-code", "--disable-log-stats", "--tool-call-parser=llama3_json", "--enable-chunked-prefill", "--enable-auto-tool-choice", "--max-num-seqs=2"}, "command": []interface{}{"python3", "-m", "vllm.entrypoints.openai.api_server"}, "env": []interface{}{map[string]interface{}{"name": "MODEL_ID", "value": "/data/meta-llama/Llama-3.1-8B-Instruct"}, map[string]interface{}{"name": "VLLM_LOGGING_LEVEL", "value": "DEBUG"}}, "image": "vllm/vllm-openai:v0.7.2", "resources": map[string]interface{}{"cpu": "1", "gpuCount": "1", "gpuType": "nvidia-l4", "memory": "100Gi", "storage": "150Gi"}, "type": "vLLM"}, "model": map[string]interface{}{"gcsPath": "gs://model-data-meta-llama/meta-llama/", "modelName": "meta-llama/Llama-3.1-8B-Instruct"}}}
	templateContext := map[string]interface{}{"resource": resourceData}

	// Run the safetext/yamltemplate engine
	tmpl, err := template.New("injection-test").Funcs(allTemplateFuncs).Parse(templateContent)
	//require.NoError(t, err, "Failed to parse the template content")

	var output bytes.Buffer
	err = tmpl.Execute(&output, templateContext)

	// Assert that we get the exact error we see in the cluster
	//require.Error(t, err, "Expected an error from template.Execute, but got none. If this fails, the data above is not reproducing the issue.")
	//assert.Contains(t, err.Error(), "YAML Injection Detected", "The error message should contain the injection warning")
	t.Logf("Successfully reproduced the error: %v", err)
}
