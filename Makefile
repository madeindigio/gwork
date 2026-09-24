# gwork build tooling. Build recipes are silenced (@) so embedded secrets
# never appear in CI logs.
#
# The OAuth client can be embedded at build time (never commit it):
#   make build GWORK_OAUTH_CLIENT_ID=... GWORK_OAUTH_CLIENT_SECRET=... GWORK_HOSTED_DOMAIN=digio.es

BINARY  := gwork
PKG     := github.com/digio/gwork-cli
BI      := $(PKG)/internal/buildinfo
BIN_DIR := bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GWORK_OAUTH_CLIENT_ID     ?=
GWORK_OAUTH_CLIENT_SECRET ?=
GWORK_HOSTED_DOMAIN       ?=

LDFLAGS := -s -w \
	-X $(BI).Version=$(VERSION) \
	-X $(BI).Commit=$(COMMIT) \
	-X $(BI).Date=$(DATE) \
	-X $(BI).OAuthClientID=$(GWORK_OAUTH_CLIENT_ID) \
	-X $(BI).OAuthClientSecret=$(GWORK_OAUTH_CLIENT_SECRET) \
	-X $(BI).HostedDomain=$(GWORK_HOSTED_DOMAIN)

# Uses an installed golangci-lint when available, otherwise runs the pinned version.
GOLANGCI_LINT_VERSION ?= v2.14.0
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || echo "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)")

GOFLAGS_BUILD := -trimpath -ldflags "$(LDFLAGS)"

.PHONY: all build install test race cover lint fmt vet tidy clean

all: fmt vet lint test build

build: ## Build bin/gwork
	@echo "go build -o $(BIN_DIR)/$(BINARY) (version $(VERSION))"
	@go build $(GOFLAGS_BUILD) -o $(BIN_DIR)/$(BINARY) ./cmd/gwork

install: ## Install gwork into GOBIN
	@go install $(GOFLAGS_BUILD) ./cmd/gwork

test: ## Run tests with the race detector
	go test -race ./...

cover: ## Run tests with coverage
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

fmt: ## Format code
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Tidy go.mod/go.sum
	go mod tidy

clean:
	rm -rf $(BIN_DIR) dist coverage.out
