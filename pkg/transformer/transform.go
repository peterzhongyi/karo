package transformer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	template "github.com/google/safetext/yamltemplate"

	v1 "github.com/GoogleCloudPlatform/karo/pkg/api/v1"

	"cloud.google.com/go/storage"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

const (
	targetRootPath = "tmp"
)

var (
	// This regex is still good for parsing a GCS URI.
	gcsRegex = regexp.MustCompile(`^\s*gs://(?P<bucket>[^/]+)/(?P<path>[^\s]+)\s*$`)
)

type newGCSFileSystemFunc func(ctx context.Context, bucket, objectPath string) (filesys.FileSystem, error)
type newEmbeddedFileSystemFunc func() (filesys.FileSystem, error)

type Transformer struct {
	registry v1.RegistryInterface

	// Hook for findConnectedResources (from objectFinder.go)
	findConnectedResourcesFunc func(context.Context, discovery.DiscoveryInterface, dynamic.Interface, *unstructured.Unstructured) ([]*unstructured.Unstructured, []*unstructured.Unstructured, error)

	// Hook for topologicalSort (from objectSort.go)
	topologicalSortFunc func([]*unstructured.Unstructured) ([]*unstructured.Unstructured, error)

	// Hook for the file system factory function
	fsProviderFunc func(ctx context.Context, path string) (filesys.FileSystem, string, error)

	// You already have this one from objectFinder tests
	populateInstanceCacheFunc func(context.Context, discovery.DiscoveryInterface, dynamic.Interface) (map[schema.GroupVersionKind]map[string]*unstructured.Unstructured, error)
}

func NewTransformer() *Transformer {
	return &Transformer{
		registry: NewIntegrationRegistry(),
	}
}

func (t *Transformer) Registry() v1.RegistryInterface {
	return t.registry
}

func (t *Transformer) Run(ctx context.Context, discoveryClient discovery.DiscoveryInterface, dynamicClient dynamic.Interface, mapper meta.RESTMapper, rClient client.Client, req ctrl.Request, obj *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	objGVK := obj.GetObjectKind().GroupVersionKind()
	log := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name, "entity", objGVK.String())

	if !t.registry.HasIntegration(objGVK) {
		log.Error(nil, "missing integration")
		return nil, fmt.Errorf("missing integration for %s", objGVK.String())
	}

	findFunc := t.findConnectedResourcesFunc
	if findFunc == nil {
		findFunc = t.findConnectedResources
	}
	referenced, referencing, err := findFunc(ctx, discoveryClient, dynamicClient, obj)
	if err != nil {
		return nil, fmt.Errorf("cannot find connected resources: %v", err)
	}
	accumulator := []*unstructured.Unstructured{obj}
	accumulator = append(accumulator, referenced...)
	accumulator = append(accumulator, referencing...)

	sortFunc := t.topologicalSortFunc
	if sortFunc == nil {
		sortFunc = t.topologicalSort
	}
	sortedAccumulator, err := sortFunc(accumulator)
	if err != nil {
		return nil, fmt.Errorf("cannot sort resources: %v", err)
	}

	// 1. Build a map of all discovered resources, keyed for easy access in the template.
	//    (This assumes names are unique across kinds in the accumulator, or use a more complex key).
	resourceMap := make(map[string]interface{})
	for _, res := range sortedAccumulator {
		// We can key by kind and name for easy lookup.
		key := fmt.Sprintf("%s/%s", res.GetKind(), res.GetName())
		log.Info("Adding resource to map", "key", key, "kind", res.GetKind(), "name", res.GetName())
		resourceMap[key] = res.Object
	}

	targetFS := filesys.MakeFsOnDisk()
	if err := targetFS.MkdirAll(targetRootPath); err != nil {
		return nil, fmt.Errorf("unable to create directory at %q: %v", targetRootPath, err)
	}

	var resourceFiles []string // Will collect full relative paths to generated files.
	var lastTemplateChain string

	context := map[string]any{
		"root":           targetRootPath,
		"chain":          "",
		"resource":       nil,
		"resources":      resourceMap,
		"k8sClient":      dynamicClient,
		"k8sMapper":      mapper,
		"k8sTypedClient": rClient,
	}
	for _, resource := range sortedAccumulator {
		context["chain"] = lastTemplateChain
		context["resource"] = resource.UnstructuredContent()
		targetRelativePath := filepath.Join(resource.GetNamespace(), resource.GetName())
		targetObjectPath := filepath.Join(targetRootPath, targetRelativePath)

		if err := t.registry.ResolveContext(ctx, resource, context); err != nil {
			return nil, fmt.Errorf("unable to resolve context for resource %v: %v", resource.GroupVersionKind().String(), err)
		}

		shouldExecuteTemplates := false

		// Condition A: Always execute templates for the primary resource that triggered the reconciliation.
		if resource.GetUID() == obj.GetUID() {
			shouldExecuteTemplates = true
		} else {
			// Condition B: It's a referenced resource. Check if we should propagate its templates.
			// We look at the integration rules for the PRIMARY object (`obj`).
			for _, refRule := range t.registry.GetReferenceRules(obj.GroupVersionKind()) {
				// Find the rule that matches the current resource in the loop
				if refRule.Group == resource.GroupVersionKind().Group && refRule.Kind == resource.GetKind() {
					// Check our new flag!
					if refRule.PropagateTemplates {
						shouldExecuteTemplates = true
						break
					}
				}
			}
		}

		if !shouldExecuteTemplates {
			log.Info("Skipping template execution for read-only reference", "kind", resource.GetKind(), "name", resource.GetName())
			continue // Skip to the next resource in the accumulator
		}

		log.Info("Executing templates for resource", "kind", resource.GetKind(), "name", resource.GetName())

		// Handle pure copy operations.
		for _, copyPath := range t.registry.GetCopyPaths(resource.GroupVersionKind()) {
			fsProvider := t.fsProviderFunc
			if fsProvider == nil {
				fsProvider = fileSystemForPath
			}
			sourceFS, rootPath, err := fsProvider(ctx, copyPath)
			if err != nil {
				return nil, fmt.Errorf("unable to get file system for path %q: %v", copyPath, err)
			}

			err = sourceFS.Walk(rootPath, func(sourcePath string, info fs.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}

				baseName := filepath.Base(sourcePath)
				if baseName == "kustomization.yaml" || baseName == "kustomization.yml" || baseName == "Kustomization" {
					return nil
				}

				targetPath := path.Join(targetObjectPath, sourcePath)
				if err := targetFS.MkdirAll(path.Dir(targetPath)); err != nil {
					return err
				}

				// We collect copied files as well, assuming they are valid YAML manifests.
				resourceFiles = append(resourceFiles, path.Join(targetRelativePath, sourcePath))
				return copyFile(sourceFS, targetFS, sourcePath, targetPath, ctx)
			})
			if err != nil {
				return nil, fmt.Errorf("error walking path %q: %v", copyPath, err)
			}
		}

		// Handle template operations.
		for _, templatePath := range t.registry.GetTemplatePaths(resource.GroupVersionKind()) {
			fsProvider := t.fsProviderFunc
			if fsProvider == nil {
				fsProvider = fileSystemForPath
			}
			sourceFS, rootPath, err := fsProvider(ctx, templatePath)
			if err != nil {
				return nil, fmt.Errorf("unable to get file system for path %q: %v", templatePath, err)
			}

			err = sourceFS.Walk(rootPath, func(sourcePath string, info fs.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}

				baseName := filepath.Base(sourcePath)
				if baseName == "kustomization.yaml" || baseName == "kustomization.yml" || baseName == "Kustomization" {
					return nil
				}

				targetPath := path.Join(targetObjectPath, sourcePath)
				if err := targetFS.MkdirAll(path.Dir(targetPath)); err != nil {
					return err
				}

				// Construct the relative path from the kustomization root (tmp) to the generated file.
				relativeFilePath := path.Join(targetRelativePath, sourcePath)
				resourceFiles = append(resourceFiles, relativeFilePath)

				return templateFile(sourceFS, targetFS, sourcePath, targetPath, context, log)
			})
			if err != nil {
				return nil, fmt.Errorf("error walking path %q: %v", templatePath, err)
			}

			lastTemplateChain = filepath.Join(targetRelativePath, rootPath)
		}
	}

	if len(resourceFiles) == 0 {
		log.Info("No resource files were generated, skipping kustomization. Reconciliation complete.")
		return []*unstructured.Unstructured{}, nil // Return an empty list and no error
	}

	// Create the apply kustomization.
	fsProvider := t.fsProviderFunc
	if fsProvider == nil {
		fsProvider = fileSystemForPath
	}
	sourceFS, rootPath, err := fsProvider(ctx, "embedded:/v1/apply")
	if err != nil {
		return nil, fmt.Errorf("unable to get apply path: %v", err)
	}

	// Use the collected *file* paths to build the root kustomization.
	if err := templateFile(sourceFS, targetFS, path.Join(rootPath, "apply.yaml"), path.Join(targetRootPath, "kustomization.yaml"), resourceFiles, log); err != nil {
		return nil, fmt.Errorf("unable to create root kustomization: %v", err)
	}

	opts := &krusty.Options{
		LoadRestrictions: types.LoadRestrictionsNone,
		PluginConfig: types.MakePluginConfig(
			types.PluginRestrictionsBuiltinsOnly, types.BploUseStaticallyLinked),
	}

	k := krusty.MakeKustomizer(opts)
	resmap, err := k.Run(targetFS, targetRootPath)
	if err != nil {
		return nil, fmt.Errorf("cannot run kustomization: %v", err)
	}

	result := []*unstructured.Unstructured{}
	for _, res := range resmap.Resources() {
		fmt.Println(res.MustYaml())
		data, err := res.Map()
		if err != nil {
			return nil, fmt.Errorf("unable to get resource data: %v", err)
		}
		u := &unstructured.Unstructured{}
		u.SetUnstructuredContent(data)

		// So what's happening is that the data key:value pair for Secret was something like this:
		// data: hf_token:fasdlkfjasljf==
		// Meaning it wasn't map already, you need to split the string
		// TODO: find a better way to handle this
		if u.GetKind() == "Secret" {
			if dataValue, ok := u.Object["data"].(string); ok {
				// Manually parse the data string (assuming single key-value pair for now)
				parts := strings.SplitN(dataValue, ":", 2)
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					u.Object["data"] = map[string]interface{}{
						key: value,
					}
				} else {
					return nil, fmt.Errorf("failed to parse Secret data stringnname: %v, dataValue: %v", u.GetName(), dataValue)
				}
			}
		}
		result = append(result, u)
	}
	return result, nil
}

func copyFile(sourceFS filesys.FileSystem, targetFS filesys.FileSystem, sourcePath string, targetPath string, ctx context.Context) error {
	log := log.FromContext(ctx)

	// Create parent directories if they don't exist
	if err := targetFS.MkdirAll(filepath.Dir(targetPath)); err != nil {
		return fmt.Errorf("failed to create target directories for %s: %w", targetPath, err)
	}

	source, err := sourceFS.Open(sourcePath)
	if err != nil {
		// Check if the error is a "file not found" error
		if os.IsNotExist(err) {
			return fmt.Errorf("source file %s does not exist: %w", sourcePath, err)
		}
		return fmt.Errorf("failed to open source file %s: %w", sourcePath, err)
	}
	defer source.Close()

	target, err := targetFS.Create(targetPath)
	if err != nil {
		log.Error(err, "copyFile - Failed to create target file", "targetPath", targetPath)
		return fmt.Errorf("failed to create target file %s: %w", targetPath, err)
	}
	defer target.Close()

	_, err = io.Copy(target, source)
	if err != nil {
		log.Error(err, "copyFile - Failed to copy", "sourcePath", sourcePath, "targetPath", targetPath)
		return fmt.Errorf("failed to copy from %s to %s: %w", sourcePath, targetPath, err)
	}

	return nil
}

func templateFile(sourceFS filesys.FileSystem, targetFS filesys.FileSystem, sourcePath string, targetPath string, context any, log logr.Logger) error {
	// Read the template and format the output to the target path.
	buffer, err := sourceFS.ReadFile(sourcePath)
	if err != nil {
		log.Error(err, "Failed to read template file", "sourcePath", sourcePath)
		return fmt.Errorf("failed to read template file %s: %w", sourcePath, err)
	}
	temp, err := template.New(targetPath).Funcs(allTemplateFuncs).Parse(string(buffer))

	if err != nil {
		log.Error(err, "Failed to parse template", "targetPath", targetPath)
		return fmt.Errorf("failed to parse template %s: %w", targetPath, err)
	}

	output := &bytes.Buffer{}
	if err := temp.Execute(output, context); err != nil {
		log.Error(err, "Failed to execute template", "targetPath", targetPath)
		return fmt.Errorf("failed to execute template %s: %w", targetPath, err)
	}

	target, err := targetFS.Create(targetPath)
	if err != nil {
		log.Error(err, "Failed to create target file", "targetPath", targetPath)
		return fmt.Errorf("failed to create target file %s: %w", targetPath, err)
	}
	defer target.Close()

	_, err = io.Copy(target, output)
	if err != nil {
		log.Error(err, "Failed to write template output to target file", "targetPath", targetPath)
		return fmt.Errorf("failed to write template output to target file %s: %w", targetPath, err)
	}
	return nil
}

// It accepts the filesystem constructors as arguments, allowing us to inject mocks.
func fileSystemForPathWithOptions(
	ctx context.Context,
	path string,
	gcsFactory newGCSFileSystemFunc,
	embeddedFactory newEmbeddedFileSystemFunc,
) (filesys.FileSystem, string, error) {
	u, err := url.Parse(path)
	fmt.Println("[DEBUG] fileSystemForPath", path) //Add this log
	if err != nil {
		return nil, "", fmt.Errorf("unable to parse URL %q: %v", path, err)
	}

	switch u.Scheme {
	case "embedded":
		fs, err := embeddedFactory()
		if err != nil {
			return nil, "", fmt.Errorf("unable to create file system; %v", err)
		}
		return fs, strings.TrimLeft(u.Path, "/"), nil

	case "gcs":
		pathParts := strings.SplitN(strings.TrimLeft(u.Path, "/"), "/", 2)
		if len(pathParts) != 2 {
			return nil, "", fmt.Errorf("unable to parse GCS path %q", u.Path)
		}
		bucket, objectPath := pathParts[0], pathParts[1]

		fs, err := gcsFactory(ctx, bucket, objectPath)
		if err != nil {
			return nil, "", fmt.Errorf("unable to create file system; %v", err)
		}
		return fs, objectPath, nil
	}

	return nil, "", fmt.Errorf("could not find file system for scheme %q", u.Scheme)
}

// fileSystemForPath is a thin wrapper that provides the REAL dependencies.
// The core logic has been moved to the testable function above.
func fileSystemForPath(ctx context.Context, path string) (filesys.FileSystem, string, error) {

	// This function contains the untestable call to storage.NewClient()
	realGCSFactory := func(ctx context.Context, bucket, objectPath string) (filesys.FileSystem, error) {
		client, err := storage.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to create storage client: %v", err)
		}
		return newGCSFileSystem(client, bucket, objectPath)
	}

	// The original function now just wires up the real dependencies.
	return fileSystemForPathWithOptions(ctx, path, realGCSFactory, newEmbeddedFileSystem)
}
