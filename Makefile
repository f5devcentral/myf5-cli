# ==============================================================================
# Makefile for myf5-cli
# High-Performance F5 Cloud & Appliance Automation CLI
# ==============================================================================

BINARY_NAME   := myf5
CMD_PATH      := ./cmd/myf5
DIST_DIR      := dist
BIN_DIR       := bin
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "1.0.0")
LDFLAGS       := -s -w -X main.Version=$(VERSION)

# Supported cross-compilation matrix
PLATFORMS     := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: all build test test-coverage fmt vet lint vulncheck fuzz sbom clean cross-compile run help

# Default target
all: fmt vet test build

## build: Build static binary for host OS and architecture
build:
	@mkdir -p $(BIN_DIR)
	@echo "==> Building $(BINARY_NAME) ($(VERSION)) for host..."
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo "==> Binary created: $(BIN_DIR)/$(BINARY_NAME)"

## sbom: Generate a CycloneDX SBOM from the built binary
sbom: build
	@./scripts/gen-sbom.sh $(BIN_DIR)/$(BINARY_NAME) $(BIN_DIR)/sbom.json

## test: Run unit tests with race detection
test:
	@echo "==> Running unit test suite with race detector..."
	go test -v -race ./...

## test-coverage: Run unit tests with coverage profile and generate HTML report
test-coverage:
	@echo "==> Running test suite with coverage..."
	@mkdir -p $(BIN_DIR)
	go test -v -race -covermode=atomic -coverprofile=$(BIN_DIR)/coverage.out ./...
	go tool cover -func=$(BIN_DIR)/coverage.out
	go tool cover -html=$(BIN_DIR)/coverage.out -o $(BIN_DIR)/coverage.html
	@echo "==> Coverage HTML report generated at $(BIN_DIR)/coverage.html"

## fmt: Format all Go source files
fmt:
	@echo "==> Running gofmt..."
	gofmt -s -w .

## vet: Run go vet static analysis
vet:
	@echo "==> Running go vet..."
	go vet ./...

## lint: Run golangci-lint
lint:
	@echo "==> Running golangci-lint..."
	@which golangci-lint > /dev/null 2>&1 || (echo "golangci-lint not installed. Run: brew install golangci-lint or visit golangci-lint.run" && exit 1)
	golangci-lint run ./...

## vulncheck: Run Go official vulnerability scanner
vulncheck:
	@echo "==> Running govulncheck..."
	@PATH="$$PATH:$$(go env GOPATH)/bin" which govulncheck > /dev/null 2>&1 || (echo "govulncheck not found. Installing..." && go install golang.org/x/vuln/cmd/govulncheck@latest)
	@PATH="$$PATH:$$(go env GOPATH)/bin" govulncheck ./...

## fuzz: Run native Go coverage-guided fuzz testing to uncover edge-case exploits
fuzz:
	@echo "==> Running Go native fuzz tests..."
	go test -fuzz=FuzzValidateRemoteAppliancePath -fuzztime=5s ./pkg/bigip
	go test -fuzz=FuzzValidateApplianceDownloadPath -fuzztime=5s ./pkg/bigip
	go test -fuzz=FuzzValidateHost -fuzztime=5s ./pkg/bigip
	go test -fuzz=FuzzCleanSSHHost -fuzztime=5s ./pkg/bigip
	go test -fuzz=FuzzInspectQKViewReader -fuzztime=5s ./pkg/bigip
	go test -fuzz=FuzzParseUploadResponse -fuzztime=5s ./pkg/ihealth
	go test -fuzz=FuzzCheckNoCLISecrets -fuzztime=5s ./pkg/auth

## cross-compile: Compile standalone static binaries for all target architectures
cross-compile: clean
	@mkdir -p $(DIST_DIR)
	@echo "==> Cross-compiling static binaries for: $(PLATFORMS)..."
	@for platform in $(PLATFORMS); do \
		OS=$${platform%/*}; \
		ARCH=$${platform#*/}; \
		SUFFIX=""; \
		if [ "$$OS" = "windows" ]; then SUFFIX=".exe"; fi; \
		OUTPUT="$(DIST_DIR)/$(BINARY_NAME)-$${OS}-$${ARCH}$${SUFFIX}"; \
		echo "    -> Building $$OUTPUT..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -trimpath -ldflags="$(LDFLAGS)" -o $$OUTPUT $(CMD_PATH) || exit 1; \
	done
	@echo "==> Generating SHA-256 checksums..."
	@cd $(DIST_DIR) && shasum -a 256 $(BINARY_NAME)-* > checksums.txt
	@echo "==> Generating SBOM..."
	@./scripts/gen-sbom.sh $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 $(DIST_DIR)/sbom.json
	@echo "==> Cross-compilation complete:"
	@ls -lh $(DIST_DIR)

## clean: Remove build artifacts, binaries, and temporary files
clean:
	@echo "==> Cleaning build artifacts..."
	@rm -rf $(BIN_DIR) $(DIST_DIR) coverage.out coverage.html
	@echo "==> Clean complete."

## run: Run the CLI locally with provided arguments (usage: make run ARGS="doctor")
run: build
	@$(BIN_DIR)/$(BINARY_NAME) $(ARGS)

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Available Targets:"
	@awk '/^[a-zA-Z\-_0-9]+:/ { \
		helpMessage = match(lastLine, /^## (.*)/); \
		if (helpMessage) { \
			helpCommand = substr($$1, 0, index($$1, ":")-1); \
			helpExplanation = substr(lastLine, RSTART + 3, RLENGTH); \
			printf "  \033[36m%-16s\033[0m %s\n", helpCommand, helpExplanation; \
		} \
	} \
	{ lastLine = $$0 }' $(MAKEFILE_LIST)
