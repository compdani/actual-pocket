package crdt

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/crdt/pb"
)

// TestProtoFieldNames pins the generated names the rest of the server uses.
func TestProtoFieldNames(t *testing.T) {
	request := &pb.SyncRequest{
		FileId:  "file",
		GroupId: "group",
		KeyId:   "key",
		Since:   "2015-04-24T22:23:42.123Z-0000-0000000000000000",
		Messages: []*pb.MessageEnvelope{
			{Timestamp: "2015-04-24T22:23:42.123Z-1000-0123456789ABCDEF", IsEncrypted: true, Content: []byte("payload")},
		},
	}

	encoded, err := proto.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded := &pb.SyncRequest{}
	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.GetFileId() != "file" || decoded.GetGroupId() != "group" || decoded.GetKeyId() != "key" || decoded.GetSince() != request.Since {
		t.Fatalf("round trip lost a field: %+v", decoded)
	}
	if len(decoded.GetMessages()) != 1 {
		t.Fatalf("messages = %d, want 1", len(decoded.GetMessages()))
	}
	message := decoded.GetMessages()[0]
	if message.GetTimestamp() != request.Messages[0].Timestamp || !message.GetIsEncrypted() || !bytes.Equal(message.GetContent(), []byte("payload")) {
		t.Errorf("message round trip = %+v", message)
	}

	response := &pb.SyncResponse{
		Merkle:   `{"hash":0}`,
		Messages: request.Messages,
	}
	encoded, err = proto.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	decodedResponse := &pb.SyncResponse{}
	if err := proto.Unmarshal(encoded, decodedResponse); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if decodedResponse.GetMerkle() != `{"hash":0}` {
		t.Errorf("merkle = %q", decodedResponse.GetMerkle())
	}
	if len(decodedResponse.GetMessages()) != 1 {
		t.Errorf("response messages = %d, want 1", len(decodedResponse.GetMessages()))
	}
}
