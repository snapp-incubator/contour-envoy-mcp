# Build stage
FROM golang:1.23-bookworm AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TAG=dev
ARG COMMIT=none
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-X main.version=${TAG} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /contour-envoy-mcp ./cmd/

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /contour-envoy-mcp /usr/local/bin/contour-envoy-mcp

USER 65532:65532

ENTRYPOINT ["contour-envoy-mcp"]
CMD ["-transport", "stdio"]
