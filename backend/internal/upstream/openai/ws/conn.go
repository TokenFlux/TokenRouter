package ws

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// conn.go models pooled WebSocket connections, lease handles, and outbound
// handshake compatibility keys for OpenAI Responses WebSocket transport. Each
// WSConn owns one occupancy token, independent read and write mutexes, atomic
// activity timestamps, and a close-once latch so active lease I/O and pool
// eviction coordinate without holding provider map locks during socket close.
//
// Compatibility keys combine destination URL, proxy, TLS profile key, sorted
// beta feature headers, and fingerprint-selected session headers while keeping
// routing hints advisory. WSConnLease guards post-release reuse at entry and
// returns or evicts its underlying socket when a turn finishes.

// WSAcquireRequest selects provider transport identity and connection reuse
// policy for one acquisition.
type WSAcquireRequest struct {
	// Provider supplies identity and concurrency/fingerprint rules; request cloning
	// shares this pointer, so its fields remain stable during acquisition and
	// retained prewarm use.
	Provider *WSPoolProvider
	// WSURL is the caller-validated WebSocket target; acquisition trims it and
	// rejects blank text while target/redirect security belongs to the transport
	// caller.
	WSURL string
	// Headers supplies stable handshake identity and routing preference;
	// acquisition clones value slices, and compatibility uses these bytes before
	// dial-time factory refresh.
	Headers http.Header
	// HeadersFactory optionally refreshes cloned headers for each actual dial;
	// callback closure state stays shared and needs its own synchronization across
	// concurrent dials.
	HeadersFactory func(context.Context, http.Header) (http.Header, error)
	ProxyURL       string // selected outbound proxy URL
	// TLSProfile is passed through to the dialer without a deep copy; callers keep
	// the profile stable while shared acquisition/prewarm requests use it.
	TLSProfile *tlsfingerprint.Profile
	// TLSProfileKey takes precedence over profile-derived identity when trimmed
	// nonblank; callers supply a stable key when randomized instances represent one
	// configured TLS profile.
	TLSProfileKey string
	// PreferredConnID is ordinary first preference unless ForcePreferredConn is
	// active; ForceNewConn bypasses that continuation selection and uses fresh-dial
	// policy.
	PreferredConnID string
	// ForceNewConn skips reuse and preferred enforcement; if no fresh slot or
	// reclaimable idle socket exists, acquisition returns queue-full without
	// waiting.
	ForceNewConn bool
	// ForcePreferredConn requires a compatible, unexpired PreferredConnID unless
	// ForceNewConn overrides it; unavailable identities fail and busy selections
	// queue only for that socket.
	ForcePreferredConn bool
}

// openAIWSHandshakeCompatibilityKey stores comparable beta and fingerprint
// identity values for socket reuse.
// betaFeatures always participates in compatibility. codexInstallationID joins
// in device/full modes; sessionIDHyphen, sessionIDUnderscore, threadID,
// clientRequestID, and codexWindowID join for other non-off/non-device modes.
// Missing selected values remain empty and still compare as tuple members.
type openAIWSHandshakeCompatibilityKey struct {
	betaFeatures        string // normalized beta feature set
	codexInstallationID string // selected installation identity value
	sessionIDHyphen     string // hyphenated session header value
	sessionIDUnderscore string // underscored session header value
	threadID            string // selected thread identity value
	clientRequestID     string // selected client-request identity value
	codexWindowID       string // selected Codex window identity
}

// WSConnLease owns one selected connection token and acquisition observations.
// Keep Conn, ProviderID, and pool association stable while using it, finish its
// I/O before Release, and do not copy a live lease containing its release
// latch.
type WSConnLease struct {
	pool       *WSConnPool // owning lease connection pool
	ProviderID int64       // selected lease provider identity
	// Conn retains the selected socket; public lease operations check release at
	// entry, so callers keep this reference stable and finish I/O before returning
	// it.
	Conn      *WSConn
	queueWait time.Duration // accumulated queue reservation duration
	connPick  time.Duration // delivered selection-loop pick duration
	reused    bool          // existing socket acquisition choice
	// released latches one token return per lease; it guards future operation entry
	// but cannot stop I/O that already passed its check.
	released atomic.Bool
}

// WSConn retains transport identity, one lease token, and independently
// synchronized direction/activity state.
// id, ws, handshakeHeaders, compatibility, and routingAffinity are filled
// before pool publication and kept stable afterward. leaseCh governs occupancy
// independently of closedCh; closeOnce publishes closure once, while direction
// locks and atomic timestamps support concurrent transport observations.
type WSConn struct {
	id string // pool-generated socket identity string
	// ws is the retained transport owned by connection closing; read/write/ping
	// errors propagate while the session decides whether to evict or release.
	ws openai.WSClientConn

	handshakeHeaders http.Header         // cloned upstream handshake headers
	compatibility    wsConnCompatibility // immutable outbound compatibility tuple
	routingAffinity  string              // captured advisory route preference

	// leaseCh holds one availability token and remains open after closure; closedCh
	// separately rejects new acquisition even when a token is returned.
	leaseCh chan struct{}
	// closedCh is published before transport Close starts; token availability and
	// completion of a close handshake can therefore lag this closure observation.
	closedCh  chan struct{}
	closeOnce sync.Once // connection disposal publication latch

	// readMu serializes reads independently of writeMu; its ordinary mutex wait is
	// not interruptible by the operation's parent or timeout context.
	readMu sync.Mutex
	// writeMu serializes writes and ping independently of reads; ping creates its
	// timeout after this lock, while contextual writes derive theirs before
	// waiting.
	writeMu sync.Mutex

	waiters atomic.Int32 // forced continuation waiter count
	// createdAtNano and lastUsedNano store independent wall-clock timestamps in
	// Unix nanoseconds. Construction initializes both together; successful read/write
	// and release update activity alone, so sampled age and idle duration have
	// distinct endpoints and retain no monotonic clock metadata.
	createdAtNano atomic.Int64 // Unix nanoseconds
	lastUsedNano  atomic.Int64 // Unix nanoseconds
	prewarmed     atomic.Bool  // protocol warmup completion marker
}

// codexFingerprintMode aliases the provider's supplied fingerprint mode
// spelling for handshake key selection.
type codexFingerprintMode = string

// wsConnCompatibility is the comparable destination, proxy, TLS, and selected
// handshake identity tuple.
type wsConnCompatibility struct {
	wsURL         string                            // trimmed WebSocket target identity
	proxyURL      string                            // trimmed outbound proxy identity
	tlsProfileKey string                            // stable TLS compatibility identity
	handshake     openAIWSHandshakeCompatibilityKey // selected handshake identity tuple
}

// NewWSConn creates a connection with one available token and current
// creation/activity timestamps. It clones handshake headers and records TLS
// compatibility; pool dialing later fills destination/proxy/identity and
// routing. The provider argument is unused, and nil transports support metadata
// fixtures but fail applicable I/O.
func NewWSConn(id string, _ int64, ws openai.WSClientConn, handshakeHeaders http.Header, profile *tlsfingerprint.Profile, profileKey string) *WSConn {
	now := time.Now()
	conn := &WSConn{
		id: id,

		ws: ws,

		handshakeHeaders: cloneHeader(handshakeHeaders),

		compatibility: wsConnCompatibility{tlsProfileKey: openAIWSTLSProfileKey(profile, profileKey)},

		leaseCh: make(chan struct{}, 1),

		closedCh: make(chan struct{}),
	}
	conn.leaseCh <- struct{}{}
	conn.createdAtNano.Store(now.UnixNano())
	conn.lastUsedNano.Store(now.UnixNano())
	return conn
}

// openAIWSTLSProfileKey prefers a nonblank trimmed supplied key over the
// profile-derived cache key. A stable supplied key can retain compatibility
// across randomized profile instances. Callers own whether that key faithfully
// identifies the intended TLS configuration.
func openAIWSTLSProfileKey(profile *tlsfingerprint.Profile, profileKey string) string {
	if key := stringsTrim(profileKey); key != "" {
		return key
	}
	return tlsfingerprint.CacheKey(profile)
}

// CloneWSAcquireRequest copies headers/value slices and trims target, proxy,
// and preferred-ID text. Other fields are shallow copies. Provider, TLSProfile,
// and HeadersFactory remain shared, so callers keep those referenced inputs
// stable while acquisition/prewarm consumes the clone.
func CloneWSAcquireRequest(req WSAcquireRequest) WSAcquireRequest {
	copied := req
	copied.Headers = cloneHeader(req.Headers)
	copied.WSURL = stringsTrim(req.WSURL)
	copied.ProxyURL = stringsTrim(req.ProxyURL)
	copied.PreferredConnID = stringsTrim(req.PreferredConnID)
	return copied
}

// CloneWSAcquireRequestPtr returns a pointer to the header-copying request
// clone, or nil for nil input. Provider/profile/callback references remain
// shared. The result's scalar fields and cloned headers can diverge
// independently from the supplied request.
func CloneWSAcquireRequestPtr(req *WSAcquireRequest) *WSAcquireRequest {
	if req == nil {
		return nil
	}
	copied := CloneWSAcquireRequest(*req)
	return &copied
}

// normalizeOpenAIWSBetaFeatures collects beta features across case-insensitive
// header names and comma-separated values, trimming, deduplicating, and sorting
// their spelling. Feature case remains significant. Sorting makes reordered
// equivalent declarations produce one comparable handshake key for reuse and
// prewarm target checks.
func normalizeOpenAIWSBetaFeatures(headers http.Header) string {
	features := make(map[string]struct{})
	for name, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(name), "x-codex-beta-features") {
			continue
		}
		for _, value := range values {
			for feature := range strings.SplitSeq(value, ",") {
				if feature = strings.TrimSpace(feature); feature != "" {
					features[feature] = struct{}{}
				}
			}
		}
	}
	if len(features) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(features))
	for feature := range features {
		normalized = append(normalized, feature)
	}
	sort.Strings(normalized)
	return strings.Join(normalized, ",")
}

// normalizeOpenAIWSHandshakeCompatibility builds a selected identity tuple from
// beta features and fingerprint mode. Off uses beta alone; device additionally
// uses installation identity; all other non-off modes include
// session/thread/request/window headers.
//
// The key retains both session-id spellings separately. Stable identity lookup
// uses canonical http.Header.Get, while beta scanning accepts name variants.
// Values are copied as strings into comparable state before reuse, allowing
// destination/transport identity to remain distinct from the advisory routing
// hint and refreshed authentication supplied at dial time.
func normalizeOpenAIWSHandshakeCompatibility(provider *WSPoolProvider, headers http.Header) openAIWSHandshakeCompatibilityKey {
	key := openAIWSHandshakeCompatibilityKey{
		betaFeatures: normalizeOpenAIWSBetaFeatures(headers),
	}
	mode := activeCodexFingerprintMode(provider)
	if mode == codexFingerprintOff {
		return key
	}
	key.codexInstallationID = normalizeOpenAIWSStableIdentityHeader(headers, "x-codex-installation-id")
	if mode == codexFingerprintDevice {
		return key
	}
	key.sessionIDHyphen = normalizeOpenAIWSStableIdentityHeader(headers, "session-id")
	key.sessionIDUnderscore = normalizeOpenAIWSStableIdentityHeader(headers, "session_id")
	key.threadID = normalizeOpenAIWSStableIdentityHeader(headers, "thread-id")
	key.clientRequestID = normalizeOpenAIWSStableIdentityHeader(headers, "x-client-request-id")
	key.codexWindowID = normalizeOpenAIWSStableIdentityHeader(headers, "x-codex-window-id")
	return key
}

// normalizeOpenAIWSStableIdentityHeader returns a trimmed canonical header
// lookup, or empty text for nil headers. It does not scan case variants
// manually. Callers normalize header insertion when fingerprint identity must
// participate in compatibility.
func normalizeOpenAIWSStableIdentityHeader(headers http.Header, name string) string {
	if headers == nil {
		return ""
	}
	return strings.TrimSpace(headers.Get(name))
}

// normalizeOpenAIWSRoutingAffinity selects the first nonblank canonical
// routing-header value, then case-insensitive name variants in sorted name
// order. Missing/blank candidates yield empty affinity. Value order within each
// header slice remains supplied.
//
// This deterministic name preference lets differently cased maps agree on the
// advisory route chosen for socket selection. The value remains a string
// preference; changing it does not invalidate otherwise compatible transport
// identity or force prewarm result disposal. Actual hard compatibility is
// represented by wsCompatibilityForRequest separately.
func normalizeOpenAIWSRoutingAffinity(headers http.Header) string {
	canonicalName := http.CanonicalHeaderKey(openAICodexRoutingHintHeader)
	if values, ok := headers[canonicalName]; ok {
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}

	variantNames := make([]string, 0)
	for name := range headers {
		if name != canonicalName && strings.EqualFold(strings.TrimSpace(name), openAICodexRoutingHintHeader) {
			variantNames = append(variantNames, name)
		}
	}
	sort.Strings(variantNames)
	for _, name := range variantNames {
		for _, value := range headers[name] {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

// cloneHeader delegates independent header map/value-slice copying, returning
// nil for nil input and writable empty storage for an empty nonnil map. Empty
// supplied value slices remain as keys with nil values. The upstream helper
// defines those representation details for handshake and request snapshots.
func cloneHeader(src http.Header) http.Header { return upstream.CloneHeader(src) }

// closeOpenAIWSConns closes each nonnil connection after callers release
// provider locking. Empty lists are ignored. Connection close-once latches
// accept repeated references, and this sequential loop can wait for each
// transport handshake before returning.
func closeOpenAIWSConns(conns []*WSConn) {
	if len(conns) == 0 {
		return
	}
	for _, conn := range conns {
		if conn == nil {
			continue
		}
		conn.close()
	}
}

// stringsTrim returns the supplied text with surrounding Unicode whitespace
// removed.
func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

// activeCodexFingerprintMode returns supplied fingerprint spelling, defaulting
// nil/empty provider state to off. Other spellings remain as supplied.
// Handshake selection treats any non-off/non-device value as requiring the
// fuller identity tuple.
func activeCodexFingerprintMode(provider *WSPoolProvider) codexFingerprintMode {
	if provider == nil || provider.FingerprintMode == "" {
		return codexFingerprintOff
	}
	return provider.FingerprintMode
}

// wsCompatibilityForRequest builds the comparable transport target from trimmed
// URL/proxy, stable TLS key, beta features, and selected fingerprint identity.
// Routing hints and authentication are excluded. Dialing, ordinary reuse,
// forced continuation, and prewarm checks consume the same tuple.
func wsCompatibilityForRequest(req WSAcquireRequest) wsConnCompatibility {
	return wsConnCompatibility{
		wsURL:         stringsTrim(req.WSURL),
		proxyURL:      stringsTrim(req.ProxyURL),
		tlsProfileKey: openAIWSTLSProfileKey(req.TLSProfile, req.TLSProfileKey),
		handshake:     normalizeOpenAIWSHandshakeCompatibility(req.Provider, req.Headers),
	}
}
