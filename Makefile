.PHONY: build test vet build-all serve mcp-smoke package package-arm64 docker-build \
	install-systemd uninstall-systemd clean

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

# Build the single merged binary (server + MCP + all CLIs) into bin/.
build-all:
	./deploy.sh build

# Run the HTTP service (JSON API + web UI) on 127.0.0.1:8765.
serve:
	go run ./cmd/antiaimark server --host 127.0.0.1 --port 8765

# One-shot MCP handshake/tools-list smoke test.
mcp-smoke:
	go run ./cmd/antiaimark mcp -V
	printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"locale":"zh-CN"}}\n{"jsonrpc":"2.0","id":2,"method":"tools/list"}\n' \
	  | go run ./cmd/antiaimark mcp | head -2

# Cross-compile a self-contained linux tarball into dist/.
package:
	./deploy.sh package amd64

package-arm64:
	./deploy.sh package arm64

docker-build:
	./deploy.sh docker-build

# Linux deployment (bare metal, systemd); see ./deploy.sh help
install-systemd:
	sudo ./deploy.sh install-systemd

uninstall-systemd:
	sudo ./deploy.sh uninstall-systemd

clean:
	rm -rf bin dist
