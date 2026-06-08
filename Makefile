.PHONY: build run test lint clean docker-build docker-push fmt vet tidy

# Build variables
BINARY     ?= contour-envoy-mcp
IMAGE      ?= ghcr.io/snapp-incubator/contour-envoy-mcp
TAG        ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -ldflags "-s -w -X main.version=$(TAG) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

test: fmt vet tidy
	golangci-lint run
	go test -v -race ./... -covermode=atomic -coverprofile=coverage.out

build: test
	CGO_ENABLED=0 go build -trimpath $(LDFLAGS) -o bin/$(BINARY) ./cmd/

run: build
	./bin/$(BINARY) -transport stdio

run-http: build
	./bin/$(BINARY) -transport streamable-http -addr :8080

clean:
	rm -rf bin/ coverage.out coverage.html

docker-build: test
	docker buildx build \
		--build-arg TAG=$(TAG) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE):$(TAG) \
		--load .

docker-push:
	docker push $(IMAGE):$(TAG)

docker-build-multiarch:
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg TAG=$(TAG) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE):$(TAG) \
		--push .
