BINARY   := orbit
MODULE   := github.com/daxroc/orbit
VERSION  := $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT)
REGISTRY := dcroche
IMAGE    := $(REGISTRY)/orbit
TAG      := $(VERSION)

.PHONY: build build-local proto tidy test docker-build docker-push docker-release helm-lint helm-template clean help

.DEFAULT_GOAL := help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: ## Build linux binary
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w $(LDFLAGS)" -o bin/$(BINARY) ./cmd/orbit

build-orbctl: ## Build orbctl linux binary
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w $(LDFLAGS)" -o bin/orbctl ./cmd/orbctl

build-local: ## Build binary for current OS
	go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/orbit

proto: ## Regenerate protobuf code (requires protoc)
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/orbit/v1/orbit.proto

tidy: ## Run go mod tidy
	go mod tidy

test: ## Run all tests with race detector
	go test ./... -v -race

docker-build: ## Build Docker image (current arch)
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(TAG) .

docker-push: ## Push Docker image
	docker push $(IMAGE):$(TAG)

docker-release: ## Build & push multi-arch image with SBOM and attestations
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--sbom=true \
		--provenance=mode=max \
		--tag $(IMAGE):$(TAG) \
		--tag $(IMAGE):latest \
		--push .

helm-lint: ## Lint Helm chart
	helm lint deploy/helm/orbit

helm-template: ## Render Helm templates locally
	helm template orbit deploy/helm/orbit

clean: ## Remove build artifacts
	rm -rf bin/
