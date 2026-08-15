# syntax=docker/dockerfile:1
# Multi-stage build: static Go binaries on a distroless, non-root runtime.
# The server binds 0.0.0.0 inside the container — map it to a loopback or
# trusted host port, and set WATERMARKS_SERVER_API_KEY for untrusted clients.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/watermarks-server ./cmd/watermarks-server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/watermarks-mcp ./cmd/watermarks-mcp && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/healthcheck ./cmd/healthcheck

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/watermarks-server /usr/local/bin/watermarks-server
COPY --from=build /out/watermarks-mcp     /usr/local/bin/watermarks-mcp
COPY --from=build /out/healthcheck        /usr/local/bin/healthcheck

ENV WATERMARKS_SERVER_VERSION=docker \
    WATERMARKS_SERVER_HOST=0.0.0.0 \
    WATERMARKS_SERVER_PORT=8765

# distroless has no shell: the entrypoint must be the exec form.
ENTRYPOINT ["/usr/local/bin/watermarks-server"]
EXPOSE 8765

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/healthcheck"]
