
IMG ?= controller:latest
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

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd:ignoreUnexportedFields=true webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

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
deploy: manifests kustomize docker-build  load-controller ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy-all
deploy-all: deploy images

.PHONY: deploy-local
deploy-local: manifests kustomize 
	$(KUSTOMIZE) build config/local | $(KUBECTL) apply -f -

.PHONY: undeploy-local
undeploy-local: manifests kustomize 
	$(KUSTOMIZE) build config/local | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Kind cluster
.PHONY: kind kind-delete kind-cluster ingress dir storage nats

kind: kind-cluster ingress dir storage nats cert-manager-install

kind-delete:
	@kind delete cluster -n jobico

kind-cluster:
	@kind create cluster -n jobico --config ./config/cluster/kind-cluster.yaml

dir:
	@docker exec -it jobico-control-plane mkdir -p /data/volumes/pv1/wasm chmod 777 /data/volumes/pv1/wasm

storage:
	@kubectl apply -f config/storage/storage.yaml

nats:
	helm install -f ./config/nats/conf.yaml nats nats/nats
	#@kubectl apply -f config/nats/nats-cluster.yaml

.PHONY: ingress wait-ingress

ingress:
	@kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/main/deploy/static/provider/kind/deploy.yaml

wait-ingress:
	@kubectl wait --namespace ingress-nginx --for=condition=ready pod --selector=app.kubernetes.io/component=controller --timeout=90s

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

wasm:
	@docker cp wasm/echo.wasm jobico-control-plane:/data/volumes/pv1/wasm

ex1: ## Deploy the sample Job "ex1"
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
.PHONY: load-image-listener compile-image-listener load-image-exec compile-image-exec listener exec images cert-manager-install load-controller execint load-image-execint compile-image-execint

images: listener exec execint

listener: compile-image-listener load-image-listener

compile-image-listener: 
	 docker build -t listener:v1 -f ./docker/dockerfile.listener .

load-image-listener: 
	kind load docker-image listener:v1 -n jobico

load-controller: 
	kind load docker-image controller:latest -n jobico

exec: compile-image-exec load-image-exec

compile-image-exec: 
	 docker build -t exec:v1 -f ./docker/dockerfile.exec .

load-image-exec:
	kind load docker-image exec:v1 -n jobico

execint: compile-image-execint load-image-execint

compile-image-execint: 
	docker build -t execint:v1 -f ./docker/dockerfile.execint .

compile-image-execint-nocache: 
	docker build --no-cache -t execint:v1 -f ./docker/dockerfile.execint .

load-image-execint:
	kind load docker-image execint:v1 -n jobico

prep-python:
	docker exec -it jobico-control-plane mkdir -p /data/volumes/pv1/python
	docker exec -it jobico-control-plane curl -o /data/volumes/pv1/python/python-3.13.0a5-wasi_sdk-20.zip -LJ https://github.com/brettcannon/cpython-wasi-build/releases/download/v3.13.0a5/python-3.13.0a5-wasi_sdk-20.zip 
	docker exec -it jobico-control-plane python3 -m zipfile -e /data/volumes/pv1/python/python-3.13.0a5-wasi_sdk-20.zip /data/volumes/pv1/python/
	docker exec -it jobico-control-plane rm /data/volumes/pv1/python/python-3.13.0a5-wasi_sdk-20.zip
#docker cp sdk/ jobico-control-plane:/data/volumes/pv1/python


# https://github.com/mozilla-spidermonkey/sm-wasi-demo/blob/main/data.json
prep-js:
	docker exec jobico-control-plane mkdir -p /data/volumes/pv1/js
	docker exec jobico-control-plane curl -o /data/volumes/pv1/js/js.wasm -LJ https://firefoxci.taskcluster-artifacts.net/OWBTW-rCTqGtqtvO4rpJkA/0/public/build/js.wasm


##@ Cert manager
.PHONY: cert-manager-install

cert-manager-install:
	kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.4/cert-manager.yaml

##@ Local certs
.PHONY: gen-certs new-certs 

new-certs: gen-certs gen-cfg-webhook

gen-certs:
	@export CAROOT=$(CERTSDIR);mkcert -cert-file=$(CERTSDIR)/tls.crt -key-file=$(CERTSDIR)/tls.key host.docker.internal 172.17.0.1

gen-ca:
	@mkdir -p $(CERTSDIR)
	@export CAROOT=$(CERTSDIR); mkcert -install

##@ Webhook local configs
.PHONY: gen-cfg-webhook gen-cfg-validating gen-cfg-mutating gen-cfg-converting

gen-cfg-webhook: gen-cfg-validating gen-cfg-mutating gen-cfg-converting

gen-cfg-validating:
	@key_base64=$$(cat certs/rootCA.pem | base64 -w 0);\
	sed "s|<key>|$$key_base64|g" config/local/webhook_validating.yaml.tmpl > config/local/webhook_validating.yaml

gen-cfg-mutating:
	@key_base64=$$(cat certs/rootCA.pem | base64 -w 0);\
	sed "s|<key>|$$key_base64|g" config/local/webhook_mutating.yaml.tmpl > config/local/webhook_mutating.yaml

gen-cfg-converting:
	@key_base64=$$(cat certs/rootCA.pem | base64 -w 0);\
	sed "s|<key>|$$key_base64|g" config/local/webhook_conversion.yaml.tmpl > config/local/webhook_conversion.yaml

##@ Dependencies

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

define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef

