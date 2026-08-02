.PHONY: tools generate lint lint-go vet build test fmt-check ci-local ci-docker

# Versiones fijadas del toolchain de CI (deben coincidir con .github/workflows/ci.yml)
GO_VERSION := 1.26.5
LINT_VERSION := v2.12.2

# Instala los plugins de generación (buf + protoc-gen-*).
tools:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Genera el código Go a partir del contrato .proto (salida versionada en gen/).
generate:
	buf generate

# Lint del contrato protobuf (distinto de lint-go, que es el linter de Go).
lint:
	buf lint

vet:
	GOWORK=off go vet ./...

build:
	GOWORK=off go build ./...

test:
	GOWORK=off go test -race ./... -count=1

# --- Red local de CI (réplica de .github/workflows/ci.yml) ---

fmt-check: ## Falla si hay archivos sin gofmt
	@unformatted=$$(GOWORK=off gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Archivos sin gofmt:"; echo "$$unformatted"; exit 1; \
	fi

lint-go: ## golangci-lint (el gate "Lint" del CI; no confundir con el lint de protobuf de arriba)
	GOWORK=off golangci-lint run --timeout=5m

.PHONY: ci-local
ci-local: fmt-check vet lint-go test build ## Pre-push: agregado de gates locales antes de mergear

.PHONY: ci-docker
ci-docker: ## Simula el CI en Docker (Go $(GO_VERSION) + golangci-lint $(LINT_VERSION)) — requiere Docker
	@docker run --rm \
		-e GOFLAGS=-buildvcs=false \
		-v "$$(go env GOPATH)/pkg/mod:/go/pkg/mod" \
		-v "$(CURDIR):/workspace" -w /workspace \
		golang:$(GO_VERSION)-bookworm \
		bash -c "set -e; curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b /usr/local/bin $(LINT_VERSION) && make ci-local"
