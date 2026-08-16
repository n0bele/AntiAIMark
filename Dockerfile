# syntax=docker/dockerfile:1
# Multi-stage build: static Go binaries on a distroless, non-root runtime.
# The server binds 0.0.0.0 inside the container — map it to a loopback or
# trusted host port, and set ANTIAIMARK_SERVER_API_KEY for untrusted clients.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/antiaimark ./cmd/antiaimark

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/antiaimark /usr/local/bin/antiaimark

ENV ANTIAIMARK_SERVER_VERSION=docker \
    ANTIAIMARK_SERVER_HOST=0.0.0.0 \
    ANTIAIMARK_SERVER_PORT=8765

# distroless has no shell: the entrypoint must be the exec form.
ENTRYPOINT ["/usr/local/bin/antiaimark", "server"]
EXPOSE 8765

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/antiaimark", "healthcheck"]
