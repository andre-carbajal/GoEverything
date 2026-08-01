.PHONY: build install test test-cover lint fmt clean build-all

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

build-all:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY)-darwin-amd64 ./cmd/ge
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY)-darwin-arm64 ./cmd/ge
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY)-linux-amd64 ./cmd/ge
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY)-linux-arm64 ./cmd/ge
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY)-windows-amd64.exe ./cmd/ge
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(GE_BINARY)-windows-arm64.exe ./cmd/ge
