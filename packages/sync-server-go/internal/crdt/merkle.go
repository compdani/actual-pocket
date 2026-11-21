package crdt

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// TrieNode is a node in a trinary radix trie keyed by the timestamp's minute in
// base 3. It mirrors the JS object in packages/crdt/src/crdt/merkle.ts, which
// means:
//
//   - a missing hash is different from a zero hash (that's why Hash is a
//     pointer),
//   - every hash is a signed int32, because JavaScript's `^` narrows the
//     unsigned murmur3 result,
//   - JSON emits "0", "1", "2" then "hash", which is the ordering
//     JSON.stringify uses for integer-like keys followed by "hash".
type TrieNode struct {
	Children [3]*TrieNode
	Hash     *int32
}

// NewTrie returns the equivalent of the JS emptyTrie(): a root with a zero
// hash and no children.
func NewTrie() *TrieNode {
	return &TrieNode{Hash: Int32(0)}
}

// Int32 returns a pointer to v, for building trie nodes.
func Int32(v int32) *int32 {
	return &v
}

// Value is the hash, treating a missing one as 0 (JS's `trie.hash || 0`).
func (n *TrieNode) Value() int32 {
	if n == nil || n.Hash == nil {
		return 0
	}
	return *n.Hash
}

// Child returns the child for the base-3 digit (0, 1 or 2), or nil.
func (n *TrieNode) Child(digit int) *TrieNode {
	if n == nil || digit < 0 || digit > 2 {
		return nil
	}
	return n.Children[digit]
}

// Insert adds a timestamp to the trie and returns a new trie. Like the JS
// insert(), the input is left untouched; unchanged subtrees are shared.
func Insert(trie *TrieNode, timestamp *Timestamp) *TrieNode {
	if trie == nil {
		trie = &TrieNode{}
	}

	hash := timestamp.Hash()
	key := strconv.FormatInt(timestamp.Millis()/1000/60, 3)

	next := trie.clone()
	next.Hash = Int32(trie.Value() ^ hash)

	return insertKey(next, key, hash)
}

// insertKey walks the remaining base-3 digits, copying only the nodes on the
// path.
func insertKey(trie *TrieNode, key string, hash int32) *TrieNode {
	if key == "" {
		return trie
	}

	digit := int(key[0] - '0')
	if digit < 0 || digit > 2 {
		// Not reachable with base-3 keys, but never corrupt the trie.
		return trie
	}

	existing := trie.Children[digit]
	child := &TrieNode{}
	if existing != nil {
		child = existing.clone()
	}

	updated := insertKey(child, key[1:], hash)
	updated.Hash = Int32(existing.Value() ^ hash)

	next := trie.clone()
	next.Children[digit] = updated

	return next
}

// Prune keeps the trie's hash plus the last two children at every level, the
// default of JS's prune(trie, n = 2).
func Prune(trie *TrieNode) *TrieNode {
	return PruneN(trie, 2)
}

// PruneN keeps the last n children (in key order) at every level. A node whose
// hash is 0 or absent is returned unchanged, children included: JS's
// `if (!trie.hash) return trie` treats 0 as falsy and pruning is lossy there.
func PruneN(trie *TrieNode, n int) *TrieNode {
	if trie == nil {
		return nil
	}
	if trie.Value() == 0 {
		return trie
	}

	next := &TrieNode{Hash: trie.Hash}

	kept := 0
	for digit := 0; digit < 3; digit++ {
		if trie.Children[digit] != nil {
			kept++
		}
	}
	firstKept := kept - n

	kept = 0
	for digit := 0; digit < 3; digit++ {
		child := trie.Children[digit]
		if child == nil {
			continue
		}
		if kept >= firstKept {
			next.Children[digit] = PruneN(child, n)
		}
		kept++
	}

	return next
}

func (n *TrieNode) clone() *TrieNode {
	copied := *n
	return &copied
}

// MarshalJSON matches JSON.stringify. Children are emitted in "0", "1", "2"
// order, then "hash"; a nil hash is omitted, a zero hash is kept.
func (n *TrieNode) MarshalJSON() ([]byte, error) {
	if n == nil {
		return []byte("null"), nil
	}

	var out bytes.Buffer
	out.WriteByte('{')

	wrote := false
	for digit := 0; digit < 3; digit++ {
		child := n.Children[digit]
		if child == nil {
			continue
		}
		if wrote {
			out.WriteByte(',')
		}
		wrote = true

		out.WriteByte('"')
		out.WriteByte(byte('0' + digit))
		out.WriteString(`":`)

		encoded, err := child.MarshalJSON()
		if err != nil {
			return nil, err
		}
		out.Write(encoded)
	}

	if n.Hash != nil {
		if wrote {
			out.WriteByte(',')
		}
		out.WriteString(`"hash":`)
		out.WriteString(strconv.FormatInt(int64(*n.Hash), 10))
	}

	out.WriteByte('}')

	return out.Bytes(), nil
}

// UnmarshalJSON reads either shape the database can hold: the empty object the
// server starts from, and anything insert() produced.
func (n *TrieNode) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*n = TrieNode{}

	for digit := 0; digit < 3; digit++ {
		encoded, ok := raw[string(rune('0'+digit))]
		if !ok {
			continue
		}
		child := &TrieNode{}
		if err := child.UnmarshalJSON(encoded); err != nil {
			return err
		}
		n.Children[digit] = child
	}

	if encoded, ok := raw["hash"]; ok {
		var hash int32
		if err := json.Unmarshal(encoded, &hash); err != nil {
			return err
		}
		n.Hash = &hash
	}

	return nil
}
