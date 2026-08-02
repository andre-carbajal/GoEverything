.PHONY: build install test test-cover lint fmt clean

BUILD_DIR := bin
GO := go
LDFLAGS := -s -w
GE_BINARY := ge

build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY) ./cmd/ge

install:
	$(GO) install ./cmd/ge

test:
	$(GO) test ./...

test-cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

lint:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf $(BUILD_DIR)
