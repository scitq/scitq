BINARY_DIR=bin
SRC_SERVER=./cmd/server
SRC_CLIENT=./cmd/client
SRC_CLI=./cmd/cli
SRC_FETCH=./cmd/fetch

BINARY_SERVER=$(BINARY_DIR)/scitq-server
BINARY_CLIENT=$(BINARY_DIR)/scitq-client
BINARY_CLI=$(BINARY_DIR)/scitq-cli
BINARY_FETCH=$(BINARY_DIR)/scitq-fetch

PLATFORMS=linux/amd64 darwin/amd64 windows/amd64
OUTDIR=bin

.PHONY: all build-server build-client build-cli build-fetch static-all static-server static-client static-cli static-fetch cross-build docs install

all: build-server build-client build-cli build-fetch

build-server:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_SERVER) $(SRC_SERVER)

build-client:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_CLIENT) $(SRC_CLIENT)

build-cli:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_CLI) $(SRC_CLI)

build-fetch:
	mkdir -p $(BINARY_DIR)
	go build -o $(BINARY_FETCH) $(SRC_FETCH)

static-all: static-server static-client static-cli static-fetch

static-server:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_SERVER)-static -a -ldflags '-extldflags "-static"' $(SRC_SERVER)

static-client:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLIENT)-static -a -ldflags '-extldflags "-static"' $(SRC_CLIENT)

static-cli:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_CLI)-static -a -ldflags '-extldflags "-static"' $(SRC_CLI)

static-fetch:
	mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_FETCH)-static -a -ldflags '-extldflags "-static"' $(SRC_FETCH)

cross-build:
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-server))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-client))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-cli))
	$(foreach platform, $(PLATFORMS), $(call build_binary, $(platform), $(word 1,$(subst /, ,$(platform))), $(word 2,$(subst /, ,$(platform))), scitq-fetch))

# Documentation generation from .proto files
docs:
	mkdir -p docs
	protoc --doc_out=./docs --doc_opt=markdown,api.md proto/*.proto

# Install compiled binaries
install: all
	install -m 755 $(BINARY_SERVER) /usr/local/bin/
	install -m 755 $(BINARY_CLIENT) /usr/local/bin/
	install -m 755 $(BINARY_CLI) /usr/local/bin/
	install -m 755 $(BINARY_FETCH) /usr/local/bin/

install2: all
	install -m 755 $(BINARY_SERVER) /usr/local/bin/scitq2-server
	install -m 755 $(BINARY_CLIENT) /usr/local/bin/scitq2-client
	install -m 755 $(BINARY_CLI) /usr/local/bin/scitq2-cli
	install -m 755 $(BINARY_FETCH) /usr/local/bin/scitq2-fetch


# Helper for cross-compilation
define build_binary
	mkdir -p $(OUTDIR)/$(1)
	GOOS=$(2) GOARCH=$(3) go build -o $(OUTDIR)/$(1)/$(4) ./cmd/$(4)
endef

