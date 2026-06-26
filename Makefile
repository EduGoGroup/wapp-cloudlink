.PHONY: tools generate lint vet build test

# Instala los plugins de generación (buf + protoc-gen-*).
tools:
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Genera el código Go a partir del contrato .proto (salida versionada en gen/).
generate:
	buf generate

# Lint del contrato protobuf.
lint:
	buf lint

vet:
	go vet ./...

build:
	go build ./...

test:
	go test ./... -count=1
