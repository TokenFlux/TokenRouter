package session

import (
	"net/textproto"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// qoder_hash.go selects explicit session identity or derives content-based
// affinity for Qoder-compatible requests. The first nonblank explicit value
// wins; requests lacking one derive a session from parsed content and source
// identity, then apply a Qoder namespace so the scheduler receives a
// platform-specific sticky hash. Canonical MIME header names are read before
// body session, conversation and prompt-cache fields.
//
// The content path parses the caller's declared wire format and binds the
// supplied SessionContext before using the shared generator. Its intermediate
// hash receives a fallback label, separating content-derived identity from an
// explicit value with the same text. An absent generated identity or parse
// failure produces no hash, leaving the caller to choose its ordinary unbound
// routing behavior.

// QoderExplicitSessionSeed returns the first nonblank supported header or body
// session value. Header lookup expects canonical MIME keys and checks each first
// value; body fields use gjson string conversion in their listed priority order.
func QoderExplicitSessionSeed(headers map[string][]string, body []byte) string {
	for _, name := range []string{"session_id", "conversation_id", "x-session-id", "x-conversation-id", "X-Claude-Code-Session-Id"} {
		values := headers[textproto.CanonicalMIMEHeaderKey(name)]
		if len(values) > 0 {
			if value := strings.TrimSpace(values[0]); value != "" {
				return value
			}
		}
	}
	for _, path := range []string{"session_id", "conversation_id", "prompt_cache_key"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

// QoderHashFromSeed derives the scheduler hash from a trimmed Qoder-labelled seed.
// Blank input returns empty, and the scheduler's legacy hash is discarded because
// Qoder callers consume the current hash returned by this helper.
func QoderHashFromSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	hash, _ := scheduler.DeriveSessionHashes("qoder:" + seed)
	return hash
}

// QoderRequestHash resolves explicit Qoder identity or a content-derived session.
// Explicit headers and body fields take precedence before protocol parsing; a
// parse failure returns empty. Otherwise the parsed request uses the supplied
// SessionContext and GenerateSessionHash's content/source rules.
//
// The generated hash is placed under a fallback label before the Qoder namespace
// is applied. This distinguishes the content path from an explicit session whose
// text happens to equal the intermediate generated hash. Empty derived identity
// returns empty, and observe is passed through for GenerateSessionHash's optional
// diagnostics.
func QoderRequestHash(headers map[string][]string, body []byte, wire string, identity *requeststate.SessionContext, observe func(string, ...any)) string {
	if seed := QoderExplicitSessionSeed(headers, body); seed != "" {
		return QoderHashFromSeed(seed)
	}
	// With no explicit session, parse the declared wire format to obtain the
	// content used by the shared hash generator; failed parsing leaves no sticky identity.
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), wire)
	if err != nil {
		return ""
	}
	parsed.SessionContext = identity
	generated := GenerateSessionHash(parsed, observe)
	if generated == "" {
		return ""
	}
	return QoderHashFromSeed("fallback:" + generated)
}
