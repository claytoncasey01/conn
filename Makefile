# The Go build carries the page bundle, so the UI is built first.
.PHONY: all ui build test check serve dev clean

all: build

node_modules: package.json bun.lock
	bun install --frozen-lockfile
	@touch node_modules

ui: node_modules
	bun run build

build: ui
	go build -o bin/conn ./cmd/conn

test: ui
	bun run test
	go test ./...

check: ui
	bun run check
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: the files above need formatting"; exit 1; }

serve: build
	bin/conn serve

dev: ui
	go run ./cmd/dev

clean:
	rm -rf bin dist
