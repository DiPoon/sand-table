GOBIN := $(shell go env GOPATH)/bin
NODE_BIN := $(CURDIR)/client/node_modules/.bin

.PHONY: test race bench build client client-dev proto tools

test:
	cd server && go vet ./... && go test ./...

race:
	cd server && go test -race ./...

# Time per tick and the costs outside the tick.
bench:
	cd server && go test ./internal/sim ./internal/view -run '^$$' -bench . -benchtime 200x

# Server binaries into bin/, and the web client into client/dist (served by sandtable-server at /).
build: client
	cd server && go build -o ../bin/ ./cmd/...

client/node_modules: client/package.json client/package-lock.json
	cd client && npm ci
	@touch $@

client: client/node_modules
	cd client && npm run build

# Vite dev server on http://127.0.0.1:5173. It forwards /ws and /api to
# sandtable-server on 8080, which must run next to it.
client-dev: client/node_modules
	cd client && npm run dev

# Regenerate server/internal/pb and client/src/gen from proto/. Generated code
# is committed, so this is only needed after changing the schema.
proto: client/node_modules
	PATH="$(GOBIN):$(NODE_BIN):$$PATH" buf lint
	PATH="$(GOBIN):$(NODE_BIN):$$PATH" buf generate

tools:
	go install github.com/bufbuild/buf/cmd/buf@v1.75.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
