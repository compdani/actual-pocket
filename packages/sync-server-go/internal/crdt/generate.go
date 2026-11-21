package crdt

// The protobuf schema is owned by the JS package; do not edit it here.
// Generated code is committed, so a normal `go build` needs no protoc.
//
//go:generate protoc --go_out=pb --go_opt=paths=source_relative --go_opt=Msync.proto=github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb -I ../../../crdt/src/proto sync.proto
