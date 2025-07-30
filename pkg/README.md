# Agent Operator

A Kubernetes operator for AI Agents.


## Getting Started

### Prerequisites
- go version v1.21.0
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.
- [helm](https://helm.sh/) version v3.12+ 
- GKE Cluster running

### Install the operator onto your K8s cluster using helm

**Once you have an Autopilot cluster up and running you can deploy the Skippy operator**

```sh
helm repo add skippy https://ai-on-gke.github.io/tutorials-and-examples/skippy-chart
helm repo update
helm install test skippy/skippy --version=0.1.0 
```
Or directly from the URL:
```sh
helm install skippy https://ai-on-gke.github.io/tutorials-and-examples/skippy-chart/skippy-0.1.0.tgz 
```

You can also install the chart from the artifact registry. 
```sh
helm install skippy oci://us-west1-docker.pkg.dev/ai-on-gke/skippy-helm-chart/skippy --version 0.1.0 
```

### To Uninstall

**UnDeploy the controller from the cluster:**

```sh
helm uninstall skippy --namespace default
```


## Testing changes

You may need to run `go mod tidy` at the root to install all modules. 

If you decide to make changes to the controller logic itself you'll need to follow this steps to test your changes.

To prepare building and pushing images, let's set the REGISTRY environment variable to point to our new project.
You can choose either Google Container Registry or Google Artifact Registry but you must set it explicitly.
For this guidance, we will use Google Artifact Registry.
You can [choose any registry region](https://cloud.google.com/artifact-registry/docs/docker/pushing-and-pulling)
but for this example, we'll just use `us-docker.pkg.dev`.
Please follow the [instructions](https://cloud.google.com/artifact-registry/docs/docker/pushing-and-pulling#before_you_begin) to create the registry
in your project properly before you contine.

In your shell, run `export REGISTRY=us-docker.pkg.dev/<YOUR-PROJECT-ID>/<YOUR-REGISTRY-NAME>` which will set the required `REGISTRY` parameters in our
Make targets.

Before we can push the images, there is one more small step! So that we can run regular `docker push` commands
(rather than `gcloud docker -- push`), we have to authenticate against the registry, which will give us a short
lived key for our local docker config. To do this, run `make gcloud-auth-docker`, and now we have the short lived tokens.

To build and push our images up at this point, is simple `make docker-build docker-push IMG=us-east4-docker.pkg.dev/<YOUR-PROJECT-ID>/<YOUR-REGISTRY-NAME>:<TAG>` and that will push up all images you just built to your
project's container registry.

Once that is done you can update the controller's deployment image name or tag accordingly. You can update either directly on the deployment resource or you can first `helm uninstall skippy` for example and then `cd install/helm` edit the `values.yaml` file and override the `image.repository` and `image.tag` with yours and do `helm install test <YOUR_LOCALLY_HELM_PACKAGE>.tgz --values values.yaml `. Additionally, if you make changes to the CRDs folder inside the helm folder you'll need to do `helm package .` to update the .tgz file so you can install the newest version.


# How to Add a New Custom Resource to Karo

This guide outlines the steps required to extend the Karo operator with a new Custom Resource Definition (CRD), enabling it to manage new kinds of applications or resources.

The core of Karo is its generic, template-driven engine. By following these steps, you can teach the operator how to reconcile a completely new resource type, often without writing any Go code (unless there's a specific type of reconciliation needed see pkg/controller/README.md).

Step 1: Define the CRD Schema
-----------------------------

The first step is to define the "shape" of your new resource. Create a CRD and include the `status` subresource (see examples/hello-world/hello_world_crd.yaml for more information). You can start by creating a folder there in examples/ so that you have a clear idea of what would be the CRD + CR. 
    

Step 2: Create the Template Directory
-------------------------------------

The generic controller needs a place to find the templates that it will use to create resources for your new CRD.

1.  **Create a New Folder:** Inside the assets/v1/ directory, create a new folder named after your resource (e.g., mynewresource). For example: `assets/v1/mynewresource/template/`
    

Step 3: Write the Kustomization Templates
-----------------------------------------

Inside the template directory, you will create the Kubernetes YAML manifests that define the resources your operator should create. These are not static YAML files; they are Go templates that will be dynamically rendered.

1.  **Create the Manifests:** Add your deployment.yaml, service.yaml, configmap.yaml, etc., to the `assets/v1/mynewresource/template/` directory.
    
2.  **Example deployment.yaml template:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata: name: {{ .resource.metadata.name }}
spec: 
   replicas: {{ .resource.spec.replicas }} 
   template: 
      spec: 
         containers: 
         - name: main-container 
           image: {{ .resource.spec.image }}
```
    

Step 4: Update the Integration Manifest
---------------------------------------

This is the most critical step. You must register your new resource with the Karo engine by adding an entry to the main Integration manifest (e.g., skippy-integrations.yaml). There's an example here of the current integration for AgenticSandbox and AgenticSandboxClass: `install/helm/templates/resources/integration.yaml`

```yaml
apiVersion: model.skippy.io/v1
kind: Integration
metadata: 
   name: skippy-integrations
spec: 
    # ... other existing integrations ... 
    # --- ADD YOUR NEW INTEGRATION HERE --- 
    - group: model.skippy.io 
      version: v1 
      kind: MyNewResource 
      templates:
        - operation: template 
          path: "{{ .Values.integration.path }}/mynewresource/template" 
           # If your CR depends on another CR (like AgenticSandbox depends on a Class), # you would add a 'references' block here. references: \[\]
```
    

Step 5: (Optional) Add a Custom Reconciler
------------------------------------------

If your new resource requires unique, **stateful** logic (like checking the status of a child resource before updating the parent), the generic controller is not enough.

In this case, you will need to:

1.  Create a new `_controller.go` file (e.g., mynewresource\_controller.go).
    
2.  Implement the ReconcileStateful method to contain your custom business logic.
    
3.  Register your new controller in `cmd/manager/main.go`.
    

For a detailed explanation of this process, refer to the README.md on the purpose of the custom `agenticsandbox_controller.go`

Step 6: Build and Deploy
------------------------

Finally, you need to build the new operator image containing your embedded templates and deploy it to the cluster.

Instructions to build and deploy via helm chart are found above
    

After these steps are complete, the Karo operator will be aware of your new resource and will be able to reconcile it according to the templates you provided.