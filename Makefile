BINARY := meshtastic-caltopo-bridge
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GOPRIVATE ?= github.com/jeremyrickard/gotopo
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

export GOPRIVATE
export GONOSUMDB := $(GOPRIVATE)

.PHONY: build test race release clean

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/bridge

test:
	go test ./...

race:
	CGO_ENABLED=1 go test -race ./...

release: clean
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(VERSION)_darwin_amd64 ./cmd/bridge
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(VERSION)_darwin_arm64 ./cmd/bridge
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(VERSION)_linux_amd64 ./cmd/bridge
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$(VERSION)_linux_arm64 ./cmd/bridge
	cd dist && if command -v sha256sum >/dev/null 2>&1; then sha256sum $(BINARY)_$(VERSION)_* > SHA256SUMS; else shasum -a 256 $(BINARY)_$(VERSION)_* > SHA256SUMS; fi

clean:
	rm -rf bin dist
