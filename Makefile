# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Golang forbids converting float from Json to struct. Need allowDangerousTypes=true to unblock build.
CRD_OPTIONS ?= "crd:maxDescLen=0,generateEmbeddedObjectMeta=true,allowDangerousTypes=true"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

# Container Engine to be used for building images
ENGINE ?= "docker"

all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

# update skippy-kustomization-templates in ai-on-gke
# This will copy the assets/v1 folder to the GCS bucket in ai-on-gke 
update-skippy-templates:
	gcloud config set project ai-on-gke
	gsutil cp -r -v assets/v1/* gs://skippy-kustomization-templates/integrations

# Upload helm chart to artifact registry
upload-helm-chart-to-artifact-registry:
	gcloud config set project ai-on-gke

	gcloud auth print-access-token | helm registry login -u oauth2accesstoken \
	--password-stdin https://us-west1-docker.pkg.dev

	helm push install/helm/skippy-0.1.0.tgz oci://us-west1-docker.pkg.dev/ai-on-gke/skippy-helm-chart

##@ Development
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=ai-connector-operator webhook paths="./pkg/api/..." output:crd:artifacts:config=config/crd/bases

generate: manifests controller-gen ## Generate code containing DeepCopy, DeepCopyInto, DeepCopyObject methods and generated client for ApplyConfiguration.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./pkg/api/..."

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	go vet ./...

test: WHAT ?= $(shell go list ./... | grep -v /test/)
test: ENVTEST_K8S_VERSION ?= 1.31.0
test: manifests fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $(WHAT) -coverprofile cover.out

##@ Build
build: fmt vet ## Build manager binary.
	go build -tags strictfipsruntime -a -o dist/manager cmd/manager/main.go

# run: manifests fmt vet
# Running the controller from your host may fail to reconcile resources.
docker-build: ## Build image only
	${ENGINE} build -t ${IMG} .

docker-push: ## Push image with the manager.
	${ENGINE} push ${IMG}

##@ Deployment

install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	($(KUSTOMIZE) build config/crd | kubectl create -f -) || ($(KUSTOMIZE) build config/crd | kubectl replace -f -)

uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/default && $(KUSTOMIZE) edit set image kuberay/operator=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply --server-side=true -f -

undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | kubectl delete -f -

##@ Binaries
## Local bin directory
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)
LOCALDIST ?= $(shell pwd)/dist
$(LOCALDIST):
	mkdir -p $(LOCALDIST)

CONTROLLER_GEN = $(LOCALBIN)/controller-gen
$(CONTROLLER_GEN): $(LOCALBIN)
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
	test -s $(CONTROLLER_GEN) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5

KUSTOMIZE = $(LOCALBIN)/kustomize
$(KUSTOMIZE): $(LOCALBIN)
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
	test -s $(KUSTOMIZE) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@v5.3.0

ENVTEST = $(LOCALBIN)/setup-envtest
$(ENVTEST): $(LOCALBIN)
envtest: $(ENVTEST) ## Download envtest locally if necessary.
	test -s $(ENVTEST) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19

.PHONY: clean
clean:
	sudo rm -rf $(LOCALBIN)
	rm -rf $(LOCALDIST)
