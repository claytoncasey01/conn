# The Go build carries the page bundle, so the UI is built first.
.PHONY: all ui build test lint fmt check serve dev clean

all: build

node_modules: package.json bun.lock
	bun install --frozen-lockfile
	@touch node_modules

ui: node_modules
	bun run build

build: ui
	go build -o bin/conn ./cmd/conn

# Go tests run with the race detector, in random order.
test: ui
	bun run test
	go test -race -shuffle=on ./...

# golangci-lint with every linter on (.golangci.yml).
lint: ui
	golangci-lint run ./...

# gofumpt and goimports.
fmt:
	golangci-lint fmt ./...

check: lint
	bun run check

serve: build
	bin/conn serve

dev: ui
	go run ./cmd/dev

clean:
	rm -rf bin dist
