.PHONY: all build test vet fmt verify-e2e keploy-record keploy-test clean

BIN_DIR := bin

all: fmt vet build test

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/flowbench-backend ./cmd/backend
	go build -o $(BIN_DIR)/flowbench-server  ./cmd/server
	go build -o $(BIN_DIR)/flowbench-client  ./cmd/client

test:
	go test -v -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

verify-e2e: build
	bash ./scripts/verify-e2e.sh

keploy-record: build
	bash ./scripts/keploy-record.sh

keploy-test: build
	bash ./scripts/keploy-test.sh

clean:
	rm -rf $(BIN_DIR) logs tmp
