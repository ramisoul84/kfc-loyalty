.PHONY: help run build test proto-gen docker

APP_NAME=kfc-loyalty
BINARY_NAME=loyalty

help:
	@echo "Available targets:"
	@echo "  run        Run the server"
	@echo "  build      Build binary to bin/loyalty"
	@echo "  test       Run unit tests"
	@echo "  proto-gen  Generate protobuf files"
	@echo "  docker     Build the Docker image"

run:
	@go run cmd/loyalty/main.go

build:
	@mkdir -p bin
	@go build -o bin/$(BINARY_NAME) cmd/loyalty/main.go
	@echo "built bin/loyalty"

test:
	@go test ./...

proto-gen:
	@echo "Generating protobuf files..."
	@find proto -name '*.proto' -print0 | xargs -0 -n1 \
		protoc \
			--go_out=. \
			--go_opt=module=github.com/ramisoul84/kfc-loyalty \
			--go-grpc_out=. \
			--go-grpc_opt=module=github.com/ramisoul84/kfc-loyalty \
			--proto_path=.
	@echo "✅ Proto files generated"

docker:
	@docker build -t $(APP_NAME):latest .
