##---------- Preliminaries ----------------------------------------------------

.POSIX:     # Get reliable POSIX behaviour
.SUFFIXES:  # Clear built-in inference rules

##---------- Variables --------------------------------------------------------

SHELL := /bin/bash

APP_NAME ?= http-probe-test-app
DOCKER_PORT ?= 8080
BIN_NAME ?= http-probe-test-app
OUT_DIR  ?= ./out
BIN_DIR  := $(OUT_DIR)/cmd/bin
OUTPUT_BIN ?= $(BIN_DIR)/$(BIN_NAME)$(ARCH)

GIT_REV = $(shell { git stash create; git rev-parse HEAD; } | grep -Exm1 '[[:xdigit:]]{40}')
SOURCE_DATE_EPOCH ?= $(shell git show -s --format="%ct" $(GIT_REV))
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || git symbolic-ref -q --short HEAD)
GIT_COMMIT ?= $(shell echo $(GIT_REV) | cut -c1-8)

LD_FLAGS="-X 'main.Version=$(VERSION)' -X 'main.GitCommit=$(GIT_COMMIT)' -s -w"

export GOCACHE ?= $(CURDIR)/.gocache
GOARCH=$(shell go env GOARCH)
GOOS=$(shell go env GOOS)
# Use Go modules
GO111MODULE := on
# Disable CGO for creating portable binaries.
CGO_ENABLED := 0
GO_FLAGS ?= -tags netgo

# Disable Go workspace mode so `./...` patterns resolve against this module only,
# not any parent go.work file (e.g. when developing inside a mono-repo).
GOWORK ?= off
GO_GOPROXY ?= https://proxy.golang.org,direct
GO_GOSUMDB ?= sum.golang.org
GO_BIN ?= $(if $(GOROOT),$(GOROOT)/bin/go,go)
GO ?= env GOWORK=$(GOWORK) GOPROXY=$(GO_GOPROXY) GOSUMDB=$(GO_GOSUMDB) $(GO_BIN)

GO_IMAGE ?= golang:1.25.10-trixie

REPORT_LINT ?= $(OUT_DIR)/report-lint.json
REPORT_VULN ?= $(OUT_DIR)/report-vuln.text

GO_BINDIR ?= $(if $(GOROOT),$(GOROOT)/bin,)
TOOL_ENV ?= env GOWORK=$(GOWORK) $(if $(GO_BINDIR),PATH="$(GO_BINDIR):$$PATH",)

LINT_FLAGS ?= --output.checkstyle.path=$(REPORT_LINT) --output.text.path=stdout --config=.golangci.yml

GOVULNCHECK_FLAGS ?= -show verbose

# Default target
.DEFAULT_GOAL := help

help: ## This message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":[^:]*?## "}; {printf "[38;5;69m%-30s[38;5;38m %s[0m\n", $$1, $$2}'

$(BIN_DIR):
	mkdir -p ${BIN_DIR}

$(OUT_DIR):
	mkdir -p ${OUT_DIR}

build: $(BIN_DIR) ## Build the binary
	$(info Building ${OUTPUT_BIN}...)
	CGO_ENABLED=0 $(GO) build --trimpath ${GO_FLAGS} -ldflags ${LD_FLAGS} -o ${OUTPUT_BIN} .

build-linux-amd: ## Build linux-amd64 binary
	@env GOOS=linux GOARCH=amd64 ARCH=-linux-amd64 make build

fmt: ## Format code
	$(GO) fmt ./...

.PHONY: vet
vet: ## Analyze code with `go vet`
	$(info ---------------------------------------------)
	$(GO) vet ./...

.PHONY: tidy
tidy: ## Make sure go.mod matches the source code in the module
	$(info ---------------------------------------------)
	$(GO) mod tidy

lint: $(OUT_DIR) ## Lint the code
	$(TOOL_ENV) golangci-lint run $(LINT_FLAGS)

# The Go race detector is used. Given that it relies on ThreadSanitizer, a C/C++ library
# that requires CGO, making CGO_ENABLED=1 becomes mandatory.
test: ## Test the code (using the Go race detector)
	$(info ---------------------------------------------)
	@$(GO) clean -testcache  # The test cache (-testcache) stores previous test results based on build flags and env vars
	env CGO_ENABLED=1 $(GO) test -race ./...

govulncheck: $(OUT_DIR) ## Check for vulnerabilities
	$(TOOL_ENV) govulncheck $(GOVULNCHECK_FLAGS) ./... | tee $(REPORT_VULN)

static-analysis: govulncheck lint ## Run govulncheck + lint

docker-build: ## Build Docker image
	docker build \
		--build-arg GO_IMAGE=$(GO_IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t $(APP_NAME):$(VERSION) .

docker-run: ## Run Docker image locally (use DOCKER_ENV_FILE=.env for extra env vars)
	docker run --rm -e PORT=$(DOCKER_PORT) -p $(DOCKER_PORT):$(DOCKER_PORT) \
		$(if $(DOCKER_ENV_FILE),--env-file $(DOCKER_ENV_FILE)) \
		$(APP_NAME):$(VERSION)

clean: ## Clean build artifacts
	$(GO) clean ./...
	rm -rf $(OUT_DIR)

.PHONY: help build build-linux-amd fmt vet tidy lint test govulncheck static-analysis docker-build docker-run clean
