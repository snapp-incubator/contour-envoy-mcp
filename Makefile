.PHONY: build run test lint clean docker-build docker-push

# Build variables
BINARY     ?= contour-envoy-mcp
IMAGE      ?= contour-envoy-mcp
TAG        ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -ldflags "-X main.version=$(TAG) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)"

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/

run: build
	./bin/$(BINARY) -transport stdio

run-http: build
	./bin/$(BINARY) -transport streamable-http -addr :8080

test:
	go test -v -race ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

docker-build:
	docker build -t $(IMAGE):$(TAG) .

docker-push:
	docker push $(IMAGE):$(TAG)

tidy:
	go mod tidy

vet:
	go vet ./...

fmt:
	gofmt -w .
	goimports -w .
