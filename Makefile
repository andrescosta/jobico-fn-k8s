IMG ?= reg.jobico.org/controller:latest
ENVTEST_K8S_VERSION = 1.29.0
CERTSDIR=./certs

ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

CONTAINER_TOOL ?= docker

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd:ignoreUnexportedFields=true webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hacks/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: test-e2e  # Run the e2e tests against a Kind k8s instance that is spun up.
test-e2e:
	go test ./test/e2e/ -v -ginkgo.v

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/opjob/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/opjob/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build --no-cache -t ${IMG} -f docker/dockerfile.op .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

PLATFORMS ?= linux/amd64
#linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' docker/dockerfile.op > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name project-v3-builder
	$(CONTAINER_TOOL) buildx use project-v3-builder
	- $(CONTAINER_TOOL) buildx build --load --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm project-v3-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	@if [ -d "config/crd" ]; then \
		$(KUSTOMIZE) build config/crd > dist/install.yaml; \
	fi
	echo "---" >> dist/install.yaml  # Add a document separator before appending
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default >> dist/install.yaml

.PHONY: build-installer-local
build-installer-local: kustomize
	mkdir -p dist-local
	@if [ -d "config/crd" ]; then \
		$(KUSTOMIZE) build config/crd > dist-local/install.yaml; \
	fi
	echo "---" >> dist-local/install.yaml
	$(KUSTOMIZE) build config/local >> dist-local/install.yaml


##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: install kustomize deploy-manifests secret docker-build docker-push  ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: deploy-manifests
deploy-manifests: manifests ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: secret
secret:
	kubectl get secret reg-cred-secret -oyaml --namespace=default | grep -v '^\s*namespace:\s' | kubectl apply -nj-system -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-all
deploy-all: deploy images nats storage fs-pod

.PHONY: deploy-local
deploy-local: manifests kustomize 
	$(KUSTOMIZE) build config/local | $(KUBECTL) apply -f -

.PHONY: undeploy-local
undeploy-local: manifests kustomize 
	$(KUSTOMIZE) build config/local | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: docker-login
docker-login:
	docker login --username jobico --password jobico123 https://reg.jobico.org

## Helpers
.PHONY: storage nats obs 

storage:
	@kubectl apply -f config/storage/storage.yaml

nats: deps-k8s-repos
	helm install -f ./config/nats/conf.yaml nats nats/nats

obs: 
	kubectl apply -f config/obs/namespace.yaml
	kubectl apply -f config/obs/grafana.yaml
	kubectl apply -f config/obs/loki.yaml
	kubectl apply -f config/obs/tempo.yaml
	kubectl apply -f config/obs/promtail.yaml

##@ Local test
.PHONY: test-op local

local: deploy-local test-op

test-op: op wasm

##@ Operator files
.PHONY: op

op: install images

##@ Echo example
.PHONY: echo wasm ex1 ex2

echo: wasm ## Copy the echo wasm files to the cluster


ex1: dir wasm ## Deploy the sample Job "ex1"
	@kubectl apply -f config/samples/1.yaml

ex2: ## Deploy the sample Job "ex2"
	@kubectl apply -f config/samples/2.yaml

ex1-delete: ## Undeploy the sample Job "ex1"
	@kubectl delete -f config/samples/1.yaml

ex2-delete: ## Undeploy the sample Job "ex2"
	@kubectl delete -f config/samples/2.yaml

logs:
	kubectl logs -f -l 'app=exec'

##@ Images
.PHONY: compile-image-listener compile-image-exec listener exec images  

images: listener exec

listener: compile-image-listener push-image-listener

compile-image-listener: 
	$(CONTAINER_TOOL) build -t listener:v1 -f ./docker/dockerfile.listener . 

push-image-listener: tag-image-listener
	$(CONTAINER_TOOL) push reg.jobico.org/listener:v1

tag-image-listener:
	$(CONTAINER_TOOL) tag listener:v1 reg.jobico.org/listener:v1

exec: compile-image-exec push-image-exec

compile-image-exec:  
	$(CONTAINER_TOOL) build -t exec:v1 -f ./docker/dockerfile.exec .

push-image-exec: tag-image-exec
	$(CONTAINER_TOOL) push reg.jobico.org/exec:v1

tag-image-exec:
	$(CONTAINER_TOOL) tag exec:v1 reg.jobico.org/exec:v1

##@ Dependencies

## Helm repos
.PHONY: deps-k8s-repos

deps-k8s-repos: ## Add helm repos
	helm repo add nats https://nats-io.github.io/k8s/helm/charts/
	helm repo update


## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize-$(KUSTOMIZE_VERSION)
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)

## Tool Versions
KUSTOMIZE_VERSION ?= v5.3.0
CONTROLLER_TOOLS_VERSION ?= v0.14.0
ENVTEST_VERSION ?= latest
GOLANGCI_LINT_VERSION ?= v1.54.2
.PHONY: deps
deps: kustomize controller-gen envtest golangci-lint cert-manager-install

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,${GOLANGCI_LINT_VERSION})

.PHONY: cert-manager-install
cert-manager-install:
	kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.4/cert-manager.yaml

define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef

.PHONY: logs delete
logs-op:
	kubectl logs -nj-system -lcontrol-plane=controller-manager
delete-op:
	kubectl delete pods -nj-system -lcontrol-plane=controller-manager
delete-pods-test:
	kubectl delete pods -levent=ev1
fs-pod:
	-kubectl apply -f config/storage/wasm-st.yaml
wait-fs-pod:
	./hacks/wait-pod.sh wasm-st
dir: fs-pod wait-fs-pod
	-@kubectl exec wasm-st -- mkdir -p /mnt/exec/wasm chmod 777 /mnt/exec/wasm
wasm:
	-@kubectl cp wasm/echo.wasm wasm-st:/mnt/exec/wasm


