# Makefile for GamerPal Discord Bot

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Binary name
BINARY_NAME=gamerpal
BINARY_PATH=./bin/$(BINARY_NAME)

# Main package path
MAIN_PATH=./cmd/gamerpal

.PHONY: all build clean test run help build-linux-amd64

# Default target
all: clean build

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	$(GOBUILD) -o $(BINARY_PATH) -v $(MAIN_PATH)

# Clean build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	@rm -rf bin/

# Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

# Run the application
run:
	@echo "Starting $(BINARY_NAME)..."
	$(GOCMD) run $(MAIN_PATH)

# Build a fully-static linux/amd64 binary suitable for distroless/static
# and used by the deploy workflows. CGO is required by the mattn/go-sqlite3
# driver, so we link statically against the host C library.
build-linux-amd64:
	@echo "Building $(BINARY_NAME) for linux/amd64..."
	@mkdir -p bin
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GOBUILD) -a -ldflags '-linkmode external -extldflags "-static"' -o bin/$(BINARY_NAME)-linux-amd64 $(MAIN_PATH)

# Show help
help:
	@echo "Available targets:"
	@echo "  build       - Build the application"
	@echo "  clean       - Clean build artifacts"
	@echo "  test        - Run tests"
	@echo "  run         - Build and run the application"
	@echo "  build-linux-amd64 - Build a static linux/amd64 binary (used by Dockerfile and deploy workflows)"
	@echo "  help        - Show this help message"
