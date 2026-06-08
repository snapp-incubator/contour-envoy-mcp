# syntax=docker/dockerfile:1.10

FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TAG=dev
ARG COMMIT=none
ARG DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w \
    -X main.version=${TAG} \
    -X main.commit=${COMMIT} \
    -X main.date=${DATE}" \
    -o /out/contour-envoy-mcp ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/contour-envoy-mcp /usr/local/bin/contour-envoy-mcp

USER nonroot:nonroot

ENTRYPOINT ["contour-envoy-mcp"]
CMD ["-transport", "stdio"]
