MODULE  := github.com/compdani/actual-pocket/packages/sync-server-go
VERSION := $(shell jq -r .version ../sync-server/package.json)

PROTO_SRC := ../crdt/src/proto

.PHONY: build test vet proto fixtures smoke clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=$(VERSION)" -o actual-server .

test: vet
	go test ./...

vet:
	go vet ./...

# Regenerates internal/crdt/pb/sync.pb.go from the JS package's schema. The
# generated file is committed.
proto:
	protoc \
		--go_out=internal/crdt/pb \
		--go_opt=paths=source_relative \
		--go_opt=Msync.proto=$(MODULE)/internal/crdt/pb \
		-I $(PROTO_SRC) \
		sync.proto

# Regenerates testdata/crdt/*.json from the JS CRDT implementation, which is
# the reference. Requires `corepack` and an installed node_modules.
fixtures:
	cd ../.. && corepack yarn workspace @actual-app/crdt build:node
	node scripts/gen-crdt-fixtures.mjs

# End-to-end smoke test: empty server, bootstrap, API upload, two syncing
# clients, backup during syncs, restore and sync again.
smoke: build
	cd ../.. && corepack yarn workspace @actual-app/api build
	node scripts/smoke.mjs

clean:
	rm -f actual-server
	rm -rf data
