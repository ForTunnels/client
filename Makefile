SHELL := /bin/bash
.ONESHELL:

export GOWORK=off

BIN_DIR ?= ./bin
ARTIFACTS_DIR ?= ./dist
VERSION ?= dev
TARGET_OS := $(or $(GOOS),$(shell go env GOOS))
BINARY_NAME := $(if $(filter windows,$(TARGET_OS)),client.msi,client)
DEFAULT_SERVER_URL ?= https://fortunnels.ru

.PHONY: all build build-fast test tidy clean release release-dev format lint security check

all: build

tidy:
	@echo "==> go mod tidy (client)"
	go mod tidy

test:
	@echo "==> go test ./..."
	go test ./...

build: check
	@echo "==> go build (client)"
	mkdir -p $(BIN_DIR)
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/client


build-fast:
	@echo "==> go build (client, fast)"
	mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN_DIR)/$(BINARY_NAME) ./cmd/client

clean:
	rm -rf $(BIN_DIR) $(ARTIFACTS_DIR)

format:
	@if [ ! -x "$(shell go env GOPATH)/bin/gofumpt" ]; then \
		echo "Installing gofumpt..."; \
		go install mvdan.cc/gofumpt@latest; \
	fi
	@if [ ! -x "$(shell go env GOPATH)/bin/goimports" ]; then \
		echo "Installing goimports..."; \
		go install golang.org/x/tools/cmd/goimports@latest; \
	fi
	@GOWORK=off $(shell go env GOPATH)/bin/gofumpt -l -w .
	@GOWORK=off $(shell go env GOPATH)/bin/goimports -w .

lint:
	@if [ ! -x "$(shell go env GOPATH)/bin/golangci-lint" ]; then \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin; \
	fi
	@GOWORK=off $(shell go env GOPATH)/bin/golangci-lint run --config .golangci.yml

security:
	@if [ ! -x "$(shell go env GOPATH)/bin/govulncheck" ]; then \
		echo "Installing govulncheck..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	@GOWORK=off $(shell go env GOPATH)/bin/govulncheck ./...

check:
	@set -euo pipefail; \
	echo "==> Running strict code checks (will fail build on any error)"; \
	echo "Checking formatting..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/gofumpt" ]; then \
		echo "Installing gofumpt..."; \
		go install mvdan.cc/gofumpt@latest; \
	fi; \
	if [ ! -x "$(shell go env GOPATH)/bin/goimports" ]; then \
		echo "Installing goimports..."; \
		go install golang.org/x/tools/cmd/goimports@latest; \
	fi; \
	$(shell go env GOPATH)/bin/gofumpt -l -w .; \
	$(shell go env GOPATH)/bin/goimports -w .; \
	go fmt ./...; \
	echo "Running go vet..."; \
	go vet ./...; \
	echo "Running tests..."; \
	go test -v ./...; \
	echo "Running golangci-lint..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/golangci-lint" ]; then \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin; \
	fi; \
	$(shell go env GOPATH)/bin/golangci-lint run --config .golangci.yml; \
	echo "Running security check..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/govulncheck" ]; then \
		echo "Installing govulncheck..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi; \
	$(shell go env GOPATH)/bin/govulncheck ./...; \
	echo "Running staticcheck..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/staticcheck" ]; then \
		echo "Installing staticcheck..."; \
		go install honnef.co/go/tools/cmd/staticcheck@latest; \
	fi; \
	$(shell go env GOPATH)/bin/staticcheck ./...; \
	echo "Checking for ineffectual assignments..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/ineffassign" ]; then \
		echo "Installing ineffassign..."; \
		go install github.com/gordonklaus/ineffassign@latest; \
	fi; \
	$(shell go env GOPATH)/bin/ineffassign ./...; \
	echo "Checking for misspellings..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/misspell" ]; then \
		echo "Installing misspell..."; \
		go install github.com/client9/misspell/cmd/misspell@latest; \
	fi; \
	$(shell go env GOPATH)/bin/misspell -error .; \
	echo "Checking cyclomatic complexity..."; \
	if [ ! -x "$(shell go env GOPATH)/bin/gocyclo" ]; then \
		echo "Installing gocyclo..."; \
		go install github.com/fzipp/gocyclo/cmd/gocyclo@latest; \
	fi; \
	$(shell go env GOPATH)/bin/gocyclo -over 15 .; \
	echo "All checks passed"

release: tidy
	@set -euo pipefail; \
	echo "==> Building client release $(VERSION)"; \
	mkdir -p "$(ARTIFACTS_DIR)"; \
	LDFLAGS="-s -w -X main.version=$(VERSION)"; \
	if [ -n "$(DEFAULT_SERVER_URL)" ]; then \
		LDFLAGS="$$LDFLAGS -X main.defaultServerURL=$(DEFAULT_SERVER_URL)"; \
	fi; \
	build_tar() { \
		OS="$$1"; ARCH="$$2"; SUFFIX="$$3"; \
		echo "   -> $$OS/$$ARCH (tar.gz)"; \
		CGO_ENABLED=0 GOOS="$$OS" GOARCH="$$ARCH" go build -ldflags "$$LDFLAGS" -o /tmp/client ./cmd/client; \
		mv /tmp/client /tmp/fortunnels; \
		tar -C /tmp -czf "$(ARTIFACTS_DIR)/fortunnels-$$SUFFIX.tar.gz" fortunnels; \
		rm -f /tmp/fortunnels; \
	}; \
	build_zip() { \
		ARCH="$$1"; SUFFIX="$$2"; \
		echo "   -> windows/$$ARCH (zip)"; \
		CGO_ENABLED=0 GOOS=windows GOARCH="$$ARCH" go build -ldflags "$$LDFLAGS" -o /tmp/client.msi ./cmd/client; \
		mv /tmp/client.msi /tmp/fortunnels.msi; \
		zip -j -q "$(ARTIFACTS_DIR)/fortunnels-$$SUFFIX.zip" /tmp/fortunnels.msi; \
		cp /tmp/fortunnels.msi "$(ARTIFACTS_DIR)/fortunnels-$$SUFFIX.msi"; \
		rm -f /tmp/fortunnels.msi; \
	}; \
	build_tar darwin amd64 macos+amd64; \
	build_tar darwin arm64 macos+arm64; \
	build_tar linux amd64 linux+amd64; \
	build_tar linux arm64 linux+arm64; \
	build_zip amd64 windows+amd64; \
	build_zip arm64 windows+arm64; \
	build_zip 386 windows+x86; \
	# MSIX fallback: Creates a copy of Windows amd64 executable as .msix \
	# for basic compatibility. For proper MSIX packaging, use: make msix-package \
	if [ ! -f "$(ARTIFACTS_DIR)/fortunnels.msix" ]; then \
		if [ -f "$(ARTIFACTS_DIR)/fortunnels-windows+amd64.msi" ]; then \
			cp "$(ARTIFACTS_DIR)/fortunnels-windows+amd64.msi" "$(ARTIFACTS_DIR)/fortunnels.msix"; \
			echo "   -> Created fallback MSIX package (copy of Windows amd64 executable)"; \
		else \
			echo "Warning: fortunnels-windows+amd64.msi not found. MSIX fallback was not created."; \
		fi; \
	fi; \
	cd "$(ARTIFACTS_DIR)" && shasum -a 256 fortunnels-* > SHA256SUMS.txt; \
	echo "==> Artifacts saved to $(ARTIFACTS_DIR)"

release-dev:
	$(MAKE) release VERSION=$(VERSION) DEFAULT_SERVER_URL=

