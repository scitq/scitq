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

.PHONY: all build-server build-client build-cli static-all static-server static-client static-cli cross-build docs install

all: build-server build-client build-cli

GIT_TAG    := $(shell git describe --tags --always --dirty)
GIT_SHA    := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
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
integration-test:
	@cd tests/integration && \
	if [ -n "$(TEST)" ]; then \
		go test -v -run '$(TEST)' ./...; \
	else \
		go test -v ./...; \
	fi

.PHONY: fresh-integration-test
fresh-integration-test:
	@cd tests/integration && \
	if [ -n "$(TEST)" ]; then \
		go test -count=1 -v -run '$(TEST)' ./...; \
	else \
		go test -count=1 -v ./...; \
	fi
