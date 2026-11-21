package crdt

import (
	"encoding/json"
	"testing"
)

type merkleFixture struct {
	Count      int      `json:"count"`
	Timestamps []string `json:"timestamps"`
	Snapshots  []struct {
		Inserted int    `json:"inserted"`
		Trie     string `json:"trie"`
		Pruned   string `json:"pruned"`
	} `json:"snapshots"`
}

// TestMerkleGolden inserts the fixture sequences into a trie that starts as the
// empty object a fresh group database has, and compares the serialised tries
// with what the JS implementation produced.
func TestMerkleGolden(t *testing.T) {
	fixtures := []string{
		"merkle-1.json",
		"merkle-10.json",
		"merkle-1000.json",
		"merkle-10000.json",
	}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			var fixture merkleFixture
			loadFixture(t, name, &fixture)

			if len(fixture.Timestamps) != fixture.Count {
				t.Fatalf("%d timestamps for count %d", len(fixture.Timestamps), fixture.Count)
			}

			// getMerkle() returns {} when the group has no merkle row yet.
			trie := &TrieNode{}

			snapshots := map[int]struct {
				trie   string
				pruned string
			}{}
			for _, snapshot := range fixture.Snapshots {
				snapshots[snapshot.Inserted] = struct {
					trie   string
					pruned string
				}{trie: snapshot.Trie, pruned: snapshot.Pruned}
			}

			for index, encoded := range fixture.Timestamps {
				timestamp, ok := Parse(encoded)
				if !ok {
					t.Fatalf("fixture timestamp %d (%q) does not parse", index, encoded)
				}

				previous := trie
				trie = Insert(trie, timestamp)

				if previous == trie {
					t.Fatalf("Insert at %d returned the same trie", index+1)
				}

				want, ok := snapshots[index+1]
				if !ok {
					continue
				}

				if got := mustJSON(t, trie); got != want.trie {
					t.Fatalf("trie after %d inserts:\n    got  %s\n    want %s", index+1, summarise(got), summarise(want.trie))
				}
				if got := mustJSON(t, Prune(trie)); got != want.pruned {
					t.Fatalf("pruned trie after %d inserts:\n    got  %s\n    want %s", index+1, summarise(got), summarise(want.pruned))
				}
			}

			if len(fixture.Snapshots) == 0 {
				t.Fatal("fixture has no snapshots")
			}
		})
	}
}

// TestMerkleInsertDoesNotMutate makes sure callers can keep using the previous
// trie, the way the JS code does.
func TestMerkleInsertDoesNotMutate(t *testing.T) {
	timestamp := mustTimestamp(t, "2015-04-24T22:23:42.123Z-1000-0123456789ABCDEF")

	before := mustJSON(t, NewTrie())
	root := NewTrie()
	after := mustJSON(t, Insert(root, timestamp))

	if before != mustJSON(t, root) {
		t.Errorf("Insert mutated its input: %s -> %s", before, mustJSON(t, root))
	}
	if after == before {
		t.Error("Insert did not change the trie")
	}
}

// TestMerkleHashCancelsToZero covers a duplicate insert: XORing the same hash
// twice zeroes the whole path, and prune() must then leave the subtree alone.
func TestMerkleHashCancelsToZero(t *testing.T) {
	timestamp := mustTimestamp(t, "2020-05-05T05:05:05.000Z-0000-ABCD")

	trie := Insert(Insert(&TrieNode{}, timestamp), timestamp)

	if trie.Value() != 0 {
		t.Fatalf("root hash = %d, want 0 after inserting the same timestamp twice", trie.Value())
	}
	for digit := 0; digit < 3; digit++ {
		if child := trie.Child(digit); child != nil && child.Value() != 0 {
			t.Errorf("child %d hash = %d, want 0", digit, child.Value())
		}
	}

	pruned := Prune(trie)
	if got := mustJSON(t, pruned); got != mustJSON(t, trie) {
		t.Errorf("prune changed a zero-hash trie:\n got  %s\n want %s", summarise(got), summarise(mustJSON(t, trie)))
	}

	// Fixing the zero hash must keep the subtree, or the next prune would lose
	// the history.
	trie.Hash = Int32(1)
	pruned = Prune(trie)
	if got := countNodes(pruned); got < countNodes(trie) {
		t.Errorf("prune with a non-zero hash dropped nodes: %d of %d kept", got, countNodes(trie))
	}
}

func TestPruneKeepsLastTwoChildren(t *testing.T) {
	leaf := func(hash int32) *TrieNode {
		return &TrieNode{Hash: Int32(hash)}
	}
	// Pick hashes that do not XOR to zero, or prune would skip the node.
	root := &TrieNode{
		Children: [3]*TrieNode{leaf(4), leaf(8), leaf(16)},
		Hash:     Int32(4 ^ 8 ^ 16),
	}

	pruned := Prune(root)

	if pruned.Child(0) != nil {
		t.Error("prune kept child 0, want only the last two")
	}
	if pruned.Child(1) == nil || pruned.Child(2) == nil {
		t.Error("prune dropped a child it should have kept")
	}
	if pruned.Value() != root.Value() {
		t.Errorf("prune changed the root hash: %d -> %d", root.Value(), pruned.Value())
	}

	// n larger than the number of children keeps them all.
	if got := countNodes(PruneN(root, 10)); got != countNodes(root) {
		t.Errorf("PruneN(root, 10) kept %d nodes, want %d", got, countNodes(root))
	}
}

func TestMerkleJSONShapes(t *testing.T) {
	cases := []struct {
		name string
		node *TrieNode
		want string
	}{
		{"empty object from the database", &TrieNode{}, "{}"},
		{"empty trie", NewTrie(), `{"hash":0}`},
		{
			"children before hash, digits in order",
			&TrieNode{
				Children: [3]*TrieNode{nil, {Hash: Int32(2)}, {Hash: Int32(3)}},
				Hash:     Int32(1),
			},
			`{"1":{"hash":2},"2":{"hash":3},"hash":1}`,
		},
		{
			"negative hash",
			&TrieNode{Hash: Int32(-1456430439)},
			`{"hash":-1456430439}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := mustJSON(t, testCase.node); got != testCase.want {
				t.Errorf("Marshal = %s, want %s", got, testCase.want)
			}

			decoded := &TrieNode{}
			if err := json.Unmarshal([]byte(testCase.want), decoded); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got := mustJSON(t, decoded); got != testCase.want {
				t.Errorf("round trip = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestMerkleJSONRoundTripsFixtures(t *testing.T) {
	var fixture merkleFixture
	loadFixture(t, "merkle-1000.json", &fixture)

	for _, snapshot := range fixture.Snapshots {
		decoded := &TrieNode{}
		if err := json.Unmarshal([]byte(snapshot.Pruned), decoded); err != nil {
			t.Fatalf("decode snapshot %d: %v", snapshot.Inserted, err)
		}
		if got := mustJSON(t, decoded); got != snapshot.Pruned {
			t.Fatalf("snapshot %d did not round-trip", snapshot.Inserted)
		}
	}
}

func mustTimestamp(t *testing.T, value string) *Timestamp {
	t.Helper()

	timestamp, ok := Parse(value)
	if !ok {
		t.Fatalf("could not parse %q", value)
	}
	return timestamp
}

func mustJSON(t *testing.T, node *TrieNode) string {
	t.Helper()

	encoded, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// summarise keeps failure output readable for the 10,000-insert fixture.
func summarise(value string) string {
	const limit = 400
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func countNodes(node *TrieNode) int {
	if node == nil {
		return 0
	}
	total := 1
	for digit := 0; digit < 3; digit++ {
		total += countNodes(node.Children[digit])
	}
	return total
}
