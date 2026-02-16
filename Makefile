BINARY_DIR=bin
SRC_SERVER=./cmd/server
SRC_CLIENT=./cmd/client
SRC_CLI=./cmd/cli
ifeq ($(OS),Windows_NT)
	EXE=.exe
else
	EXE=
endif

BINARY_SERVER=$(BINARY_DIR)/scitq-server$(EXE)
BINARY_CLIENT=$(BINARY_DIR)/scitq-client$(EXE)
BINARY_CLI=$(BINARY_DIR)/scitq$(EXE)

PLATFORMS=linux/amd64 darwin/amd64 windows/amd64
OUTDIR=bin

.PHONY: all build-server build-client build-cli static-all static-server static-client static-cli cross-build docs install add-py-version tgz-python-src

all: tgz-python-src build-server build-client build-cli

GIT_TAG    := $(shell git describe --tags --always --dirty)
GIT_SHA    := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")


# Normalize version for Python (PEP 440 compliant)
normalize_pep440 = git describe --tags --always --dirty | \
	sed -E 's/^v//' | \
	sed -E 's/-dirty/.dev0/; s/-([0-9]+)-g([0-9a-f]+)/.dev\1+g\2/; s/-/./g; /^[0-9a-f]{3,40}$$/s/^/0.0.0+/'
PY_PEP440_TAG = $(shell $(normalize_pep440))

LDFLAGS    := -X 'github.com/scitq/scitq/internal/version.Version=$(GIT_TAG)' \
              -X 'github.com/scitq/scitq/internal/version.Commit=$(GIT_SHA)' \
              -X 'github.com/scitq/scitq/internal/version.Date=$(BUILD_DATE)'
STATIC_LDFLAGS := $(LDFLAGS) -extldflags "-static"


$(BINARY_DIR):
	@mkdir -p $(BINARY_DIR)

build-server: | $(BINARY_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_SERVER) $(SRC_SERVER)

build-client: | $(BINARY_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_CLIENT) $(SRC_CLIENT)

build-cli: | $(BINARY_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_CLI) $(SRC_CLI)

static-all: static-server static-client static-cli

static-server:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_SERVER)-static -a -ldflags "$(STATIC_LDFLAGS)" $(SRC_SERVER)

static-client:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLIENT)-static -a -ldflags "$(STATIC_LDFLAGS)" $(SRC_CLIENT)

static-cli:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLI)-static -a -ldflags "$(STATIC_LDFLAGS)" $(SRC_CLI)

cross-build:
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-server))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-client))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-cli))

# Documentation generation from .proto files
docs:
	mkdir -p docs
	protoc --doc_out=./docs --doc_opt=markdown,api.md proto/*.proto


# Generate Go code from proto definitions
.PHONY: proto
proto:
	protoc --go_out=. --go-grpc_out=. --proto_path=proto proto/taskqueue.proto

# Generate Python gRPC stubs
.PHONY: proto-python
proto-python:
	@echo "Generating Python gRPC stubs..."
	@cd python && \
	mkdir -p src/scitq2/pb && \
	python3 -m grpc_tools.protoc \
	  -I ../proto \
	  --python_out=src/scitq2/pb \
	  --grpc_python_out=src/scitq2/pb \
	  --proto_path=../proto \
	  --experimental_allow_proto3_optional \
	  ../proto/taskqueue.proto && \
	sed -i '' 's/^import taskqueue_pb2/from . import taskqueue_pb2/' src/scitq2/pb/taskqueue_pb2_grpc.py
	@echo "✓ Python stubs generated in python/src/scitq2/pb/"

# Generate Svelte (TypeScript) gRPC stubs
.PHONY: proto-ui
proto-ui:
	@echo "Generating Svelte/TypeScript gRPC stubs..."
	@cd ui && npm run gen-proto
	@echo "✓ UI TypeScript stubs generated."

# Generate all gRPC stubs (Go, Python, UI)
.PHONY: proto-all
proto-all: proto proto-python proto-ui
	@echo "✓ All language stubs regenerated from proto definitions."

# --- UI build/embed (opt-out with SKIP_UI=1) -------------------------------
.PHONY: ui-deps ui-build ui-embed

UI_DIR := ui
UI_DIST := $(UI_DIR)/dist
SERVER_PUBLIC := server/public
UI_VERSION_FILE := $(UI_DIR)/src/version.ts

build-ui-version-file:
	@{ \
	  echo "// generated at build time"; \
	  echo "export const APP_VERSION = '$(GIT_TAG)';"; \
	  echo "export const APP_COMMIT  = '$(GIT_SHA)';"; \
	  echo "export const APP_BUILDT  = '$(BUILD_DATE)';"; \
	  echo "export const uiVersion = APP_VERSION;"; \
	} > $(UI_VERSION_FILE)

ui-deps:
	@cd $(UI_DIR) && if [ -f package-lock.json ]; then npm ci; else npm install; fi

ui-build: ui-deps build-ui-version-file
	@cd $(UI_DIR) && npm run build

ui-embed: ui-build
	@rm -rf $(SERVER_PUBLIC)/*
	@mkdir -p $(SERVER_PUBLIC)
	@cp -r $(UI_DIST)/* $(SERVER_PUBLIC)/

# decide if install should build UI
ifeq ($(SKIP_UI),1)
UI_PREREQ :=
else
UI_PREREQ := ui-embed
endif

# --- Python DSL version embedding -------------------------------------------
PY_DIR := python
PY_SRC := $(PY_DIR)/src/scitq2
PY_VERSION_FILE := $(PY_SRC)/__version__.py
PY_TGZ := $(PY_DIR)/python-src.tgz
PY_PROTO := $(PY_DIR)/pyproject.proto
PY_PROJECT := $(PY_DIR)/pyproject.toml

add-py-version:
	@mkdir -p $(PY_SRC)
	@{ \
	  echo "# generated at build time"; \
	  echo "__version__ = '$(PY_PEP440_TAG)'"; \
	  echo "__commit__ = '$(GIT_SHA)'"; \
	  echo "__build_time__ = '$(BUILD_DATE)'"; \
	} > $(PY_VERSION_FILE)
	@echo "✓ Updated $(PY_VERSION_FILE)"
	# Normalize version string to be PEP 440 compliant before writing to pyproject.toml
	@norm_version="$(PY_PEP440_TAG)"; \
	sed "s/^version = .*/version = \"$$norm_version\"/" $(PY_PROTO) > $(PY_PROJECT); \
	echo "✓ Generated $(PY_PROJECT) with version $$norm_version"

# Package Python source tree into an embedded zip (for Go embedding)
tgz-python-src: add-py-version
	@echo "Zipping Python DSL source..."
	@cd $(PY_DIR) && tar -czf python-src.tgz src pyproject.toml
	@echo "✓ Created $(PY_TGZ)"

install: $(UI_PREREQ) all
ifeq ($(OS),Windows_NT)
	@echo Installing binaries to %USERPROFILE%\bin...
	@powershell -Command "New-Item -ItemType Directory -Force -Path \"$$env:USERPROFILE\\bin\" | Out-Null"
	@powershell -Command "Copy-Item -Force '$(BINARY_SERVER)' \"$$env:USERPROFILE\\bin\\scitq-server.exe\""
	@powershell -Command "Copy-Item -Force '$(BINARY_CLIENT)' \"$$env:USERPROFILE\\bin\\scitq-client.exe\""
	@powershell -Command "Copy-Item -Force '$(BINARY_CLI)'    \"$$env:USERPROFILE\\bin\\scitq.exe\""
	@powershell -Command "$$userBin=[System.Environment]::ExpandEnvironmentVariables('%USERPROFILE%\bin'); $$path=[Environment]::GetEnvironmentVariable('PATH','User'); if (!($$path.Split(';') -contains $$userBin)) { [Environment]::SetEnvironmentVariable('PATH', $$path + ';' + $$userBin, 'User'); Write-Output 'PATH updated (persisted)'; } else { Write-Output 'Already in PATH.' }"
	@echo To reload your PATH now, run this in PowerShell:
	@echo   $$env:PATH = [System.Environment]::GetEnvironmentVariable('PATH','User') + ';' + [System.Environment]::GetEnvironmentVariable('PATH','Machine')
else
	@echo "Installing binaries to /usr/local/bin..."
	install -m 755 $(BINARY_SERVER) /usr/local/bin/
	install -m 755 $(BINARY_CLIENT) /usr/local/bin/
	install -m 755 $(BINARY_CLI)    /usr/local/bin/
endif

install2: all
	install -m 755 $(BINARY_SERVER) /usr/local/bin/scitq2-server
	install -m 755 $(BINARY_CLIENT) /usr/local/bin/scitq2-client
	install -m 755 $(BINARY_CLI) /usr/local/bin/scitq2



# Helper for cross-compilation
define build_binary
	mkdir -p $(OUTDIR)/$(1)
	GOOS=$(2) GOARCH=$(3) go build -o $(OUTDIR)/$(1)/$(4) ./cmd/$(4)
endef

.PHONY: integration-test
integration-test: tgz-python-src
	@cd tests/integration && \
	if [ -n "$(TEST)" ]; then \
		go test -v -run '$(TEST)' ./...; \
	else \
		go test -v ./...; \
	fi

.PHONY: fresh-integration-test
fresh-integration-test: tgz-python-src
	@cd tests/integration && \
	if [ -n "$(TEST)" ]; then \
		go test -count=1 -v -run '$(TEST)' ./...; \
	else \
		go test -count=1 -v ./...; \
	fi

config-doc:
	go run tools/gen_config_doc.go > docs/reference/configuration.md

venv: add-py-version
ifndef VENV
	$(error Please specify a virtual environment name, e.g. `make venv VENV=~/venvs/scitq2`)
endif
	python3 -m venv --clear $(VENV)
	$(VENV)/bin/python -m pip install -e ./python

dsl-doc: venv
	@echo "Generating Python DSL..."
	@python ./python/tools/gen_dsl_doc.py 

api-docs:
	protoc --doc_out=docs --doc_opt=markdown,api.md proto/taskqueue.proto
