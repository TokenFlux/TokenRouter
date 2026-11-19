package session

import (
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

// digest_session_store.go stores Gemini session bindings and matches extending
// digest chains within group and source namespaces. Lookup tries the full chain
// first, then removes complete final components until a live binding matches.
// The selected prefix is returned so the caller can retire it after saving the
// conversation's longer chain.
//
// Save publishes the replacement before deleting a different old key. Those
// map operations are individually synchronized, but their sequence is not
// atomic and can remove an old key replaced by another caller. The default
// constructor starts no cleanup goroutine; reads exclude and evict expired
// values on lookup, and the application's timing wheel calls
// ExpireRuntimeCaches to sweep unread expired entries physically.

// digestSessionTTL gives new digest bindings a five-minute default lifetime so
// inactive conversation identities eventually stop matching and can be physically removed.
const digestSessionTTL = 5 * time.Minute

// sessionEntry holds the session UUID and provider for one digest binding.
type sessionEntry struct {
	uuid       string // supplied conversation session UUID
	providerID int64  // bound upstream provider identifier
}

// DigestSessionStore holds process-local group/source digest bindings for continuation.
// Construct it with NewDigestSessionStore before Find or Save; a nonnil zero-valued
// store has no usable cache, while nil receivers skip those operations.
type DigestSessionStore struct {
	// cache is initialized by NewDigestSessionStore; lookups evict expired keys on
	// observation and the application schedules physical sweeps via ExpireRuntimeCaches.
	cache *querycache.TTLMap[*sessionEntry]
}

// NewDigestSessionStore creates an empty five-minute digest cache without a
// background janitor. Reads check expiration lazily, while the application
// schedules ExpireRuntimeCaches to sweep unread expired entries from this store.
func NewDigestSessionStore() *DigestSessionStore {
	return &DigestSessionStore{
		cache: querycache.NewTTLMap[*sessionEntry](digestSessionTTL),
	}
}

// Save binds a complete digest chain to the supplied session UUID and provider.
// A nil store or empty chain is ignored; a nonnil store needs an initialized
// cache. IDs and namespace text are used as supplied, including an empty UUID.
//
// The new key is published before a distinct oldDigestChain is removed from the
// same namespace. Callers pass Find's matched chain to keep one evolving binding
// in sequential use. SetDefault and Delete remain separate operations, so concurrent
// same-session writers can remove another caller's replacement; an unchanged old
// chain is retained to avoid deleting the freshly stored value.
func (s *DigestSessionStore) Save(groupID int64, prefixHash, digestChain, uuid string, providerID int64, oldDigestChain string) {
	if s == nil || digestChain == "" {
		return
	}
	ns := buildNS(groupID, prefixHash)
	// Publish the longer conversation chain before retiring its matched prefix
	// so continued lookup can discover the replacement during this separate-operation sequence.
	s.cache.SetDefault(ns+digestChain, &sessionEntry{uuid: uuid, providerID: providerID})
	if oldDigestChain != "" && oldDigestChain != digestChain {
		s.cache.Delete(ns + oldDigestChain)
	}
}

// Find returns the longest live digest prefix's UUID, provider, matched chain,
// and presence flag within the supplied group/source namespace. A nil store or
// empty chain returns no match; a nonnil store needs an initialized cache. Stored
// empty UUIDs or nonpositive provider IDs are still returned as found.
//
// Lookup removes final hyphen-delimited components one at a time, so callers
// supply the digest builders' role/hash chain format. Expired or nil entries
// allow shorter prefixes to be tried. Get evicts expired keys observed during
// lookup, while ExpireRuntimeCaches sweeps unread expired entries on schedule.
func (s *DigestSessionStore) Find(groupID int64, prefixHash, digestChain string) (uuid string, providerID int64, matchedChain string, found bool) {
	if s == nil || digestChain == "" {
		return "", 0, "", false
	}
	ns := buildNS(groupID, prefixHash)
	chain := digestChain
	for {
		if e, ok := s.cache.Get(ns + chain); ok && e != nil {
			return e.uuid, e.providerID, chain, true
		}
		// Step to the prior complete transcript prefix when the longer candidate
		// misses, letting an appended Gemini turn reuse the saved conversation binding.
		i := strings.LastIndex(chain, "-")
		if i < 0 {
			return "", 0, "", false
		}
		chain = chain[:i]
	}
}

// buildNS prefixes a digest key with the decimal group and supplied source hash.
// The colon and vertical-bar format expects the caller's generated prefix hash;
// values are not escaped or validated here.
func buildNS(groupID int64, prefixHash string) string {
	return strconv.FormatInt(groupID, 10) + ":" + prefixHash + "|"
}

// ExpireRuntimeCaches removes expired cache entries when the application invokes
// its timing-wheel task. Nil stores or missing caches are ignored; lookup already
// excludes expired values before this physical cleanup runs.
func (s *DigestSessionStore) ExpireRuntimeCaches() {
	if s != nil {
		if s.cache != nil {
			s.cache.DeleteExpired()
		}
	}
}
