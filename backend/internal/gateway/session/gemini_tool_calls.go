package session

import (
	"container/list"
	"sync"
	"time"
)

// gemini_tool_calls.go retains Gemini tool replay metadata behind the client
// call ID. Messages, Chat, and Responses clients already return that ID with
// the tool result. The gateway can restore the original Gemini ID and opaque
// signature from that reference, even when a client changes protocol. Reads
// are scoped to the API Key, selected provider, and upstream model so a
// different destination does not inherit another provider's signature.
//
// A process-local FIFO bounds both the number of bindings and the bytes of
// retained strings. Expired bindings are removed on lookup or the next
// record. A miss leaves the caller free to use its legacy tool history.
// The store never retains tool arguments or results, and lookup does not
// consume the binding because clients can replay the same tool history.

const (
	// Gemini tool bindings expire after an hour. Limit the retained metadata
	// by entry count and string bytes even when requests arrive faster than expiry.
	geminiToolCallTTL        = time.Hour
	geminiToolCallMaxEntries = 16384
	geminiToolCallMaxBytes   = 32 << 20
)

// GeminiToolScope identifies the key and destination allowed to replay a call.
type GeminiToolScope struct {
	APIKeyID   int64  // authenticated gateway key
	ProviderID int64  // selected upstream provider
	Model      string // mapped upstream model
}

// GeminiToolCalls stores scoped bindings for recently emitted Gemini tool calls.
type GeminiToolCalls struct {
	// mu protects the bindings, FIFO order, and byte count together so expiry
	// and replacement cannot leave an older record behind under the same ID.
	mu      sync.Mutex
	entries map[geminiToolKey]*list.Element // call keys to live FIFO entries
	order   list.List                       // bindings in record order
	bytes   int                             // retained string byte count
}

// geminiToolKey addresses a client call within its permitted replay scope.
type geminiToolKey struct {
	scope    GeminiToolScope // authenticated replay destination
	clientID string          // client tool invocation identifier
}

// geminiToolEntry retains the original wire fields and their expiry deadline.
type geminiToolEntry struct {
	key       geminiToolKey // index key for eviction
	id        string        // original Gemini call identifier
	signature string        // original Gemini thought signature
	expires   time.Time     // exclusive replay deadline
	size      int           // retained string bytes
}

// Remember stores the original Gemini fields before a tool can be replayed.
// Invalid scopes and values larger than the cache budget leave no entry.
//
// Recording evicts expired or oldest entries while holding the mutex so
// concurrent lookups cannot observe an entry after it has left the index.
// An empty ID and signature still represent an observed unsigned call. That
// hit is distinct from missing metadata because the converter only applies
// its compatibility signature to unknown calls.
func (s *GeminiToolCalls) Remember(scope GeminiToolScope, clientID, upstreamID, signature string) {
	size := len(scope.Model) + len(clientID) + len(upstreamID) + len(signature)
	if scope.APIKeyID <= 0 || scope.ProviderID <= 0 || scope.Model == "" || clientID == "" || size > geminiToolCallMaxBytes {
		return
	}
	key := geminiToolKey{scope: scope, clientID: clientID}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.entries == nil {
		s.entries = make(map[geminiToolKey]*list.Element)
	}
	if existing := s.entries[key]; existing != nil {
		s.remove(existing)
	}
	// Oldest entries expire first. Keep removing them while the new
	// binding would exceed either the entry limit or the byte budget.
	for oldest := s.order.Front(); oldest != nil; oldest = s.order.Front() {
		entry, _ := oldest.Value.(geminiToolEntry)
		if now.Before(entry.expires) && len(s.entries) < geminiToolCallMaxEntries && s.bytes+size <= geminiToolCallMaxBytes {
			break
		}
		s.remove(oldest)
	}
	s.entries[key] = s.order.PushBack(geminiToolEntry{
		key: key, id: upstreamID, signature: signature, expires: now.Add(geminiToolCallTTL), size: size,
	})
	s.bytes += size
}

// Lookup returns the original call fields for the same scope while they are
// live. A miss returns empty strings and false, including after expiration.
func (s *GeminiToolCalls) Lookup(scope GeminiToolScope, clientID string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	element := s.entries[geminiToolKey{scope: scope, clientID: clientID}]
	if element == nil {
		return "", "", false
	}
	entry, _ := element.Value.(geminiToolEntry)
	if !time.Now().Before(entry.expires) {
		s.remove(element)
		return "", "", false
	}
	return entry.id, entry.signature, true
}

// remove retires an indexed entry and accounts for its retained bytes.
// Callers hold the mutex and supply an element from this store's FIFO.
func (s *GeminiToolCalls) remove(element *list.Element) {
	entry, _ := element.Value.(geminiToolEntry)
	delete(s.entries, entry.key)
	s.order.Remove(element)
	s.bytes -= entry.size
}
