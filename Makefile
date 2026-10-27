.PHONY: all build test vet fmt verify-e2e monitor clean

BIN_DIR := bin

all: fmt vet build test

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/flowbench-backend ./cmd/backend
	go build -o $(BIN_DIR)/flowbench-server  ./cmd/server
	go build -o $(BIN_DIR)/flowbench-client  ./cmd/client
	go build -o $(BIN_DIR)/flowbench-monitor ./cmd/monitor

test:
	go test -v -race ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

verify-e2e: build
	bash ./scripts/verify-e2e.sh

monitor: build
	bash ./scripts/start-monitor.sh

clean:
	rm -rf $(BIN_DIR) logs tmp
