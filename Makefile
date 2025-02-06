BINARY_DIR=bin
SRC_SERVER=./cmd/server
SRC_CLIENT=./cmd/client
SRC_CLI=./cmd/cli

BINARY_SERVER=$(BINARY_DIR)/scitq-server
BINARY_CLIENT=$(BINARY_DIR)/scitq-client
BINARY_CLI=$(BINARY_DIR)/scitq-cli

PLATFORMS=linux/amd64 darwin/amd64 windows/amd64
OUTDIR=bin

.PHONY: all build-server build-client build-cli static-all static-server static-client static-cli cross-build docs install

all: build-server build-client build-cli docs

build-server:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_SERVER) $(SRC_SERVER)

build-client:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_CLIENT) $(SRC_CLIENT)

build-cli:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_CLI) $(SRC_CLI)

static-all: static-server static-client static-cli

static-server:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_SERVER)-static -a -ldflags '-extldflags "-static"' $(SRC_SERVER)

static-client:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLIENT)-static -a -ldflags '-extldflags "-static"' $(SRC_CLIENT)

static-cli:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLI)-static -a -ldflags '-extldflags "-static"' $(SRC_CLI)

cross-build:
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-server))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-client))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-cli))

# Documentation generation from .proto files
docs:
	mkdir -p docs
	protoc --doc_out=./docs --doc_opt=markdown,api.md proto/*.proto

# Install compiled binaries
install: all
	install -m 755 $(BINARY_SERVER) /usr/local/bin/
	install -m 755 $(BINARY_CLIENT) /usr/local/bin/
	install -m 755 $(BINARY_CLI) /usr/local/bin/

# Helper for cross-compilation
define build_binary
	mkdir -p $(OUTDIR)/$(1)
	GOOS=$(2) GOARCH=$(3) go build -o $(OUTDIR)/$(1)/$(4) ./cmd/$(4)
endef

