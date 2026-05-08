# Regenerate protobuf / gRPC stubs (requires protoc 3.21+, protoc-gen-go, protoc-gen-go-grpc on PATH).
.PHONY: proto
proto:
	protoc -I proto -I /usr/include \
		--go_out=. --go_opt=module=github.com/0xarchiviste/lighter-mcp \
		--go-grpc_out=. --go-grpc_opt=module=github.com/0xarchiviste/lighter-mcp \
		proto/mcp/v1/mcp.proto proto/lighter/v1/sdk.proto

.PHONY: test build
build:
	go build -o lighter-mcp ./cmd/lighter-mcp

test:
	go test ./...
