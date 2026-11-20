package provider

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// antigravity_authorization_sessions.go retains Antigravity state, PKCE proof,
// proxy routing, and recoverable exchanged tokens during code exchange.
// The typed store uses authorizationSessionStore to check creation-time expiry
// and remove expired entries while AntigravityAuthorization owns the exchange workflow.

// AntigravitySessionTTL bounds authorization proof reuse while Get checks age
// and periodic cleanup removes expired entries.
const AntigravitySessionTTL = 30 * time.Minute

// AntigravityAuthorizationSession retains temporary OAuth proof, optional
// project selection, and any cached exchanged token awaiting Project ID completion.
type AntigravityAuthorizationSession struct {
	State        string `json:"state"`                // generated OAuth state proof
	CodeVerifier string `json:"code_verifier"`        // stored PKCE verifier text
	ProxyURL     string `json:"proxy_url,omitempty"`  // selected authorization proxy URL
	ProjectID    string `json:"project_id,omitempty"` // optional operator-supplied GCP project ID
	// ExchangedToken, ExchangedCodeHash, and ExchangedAt cache a successfully
	// exchanged Google token when project discovery requires operator input or
	// verification, allowing retry with the same code without a second login.
	ExchangedToken    *google.AntigravityTokenResponse `json:"-"` // cached exchanged token response
	ExchangedCodeHash string                           `json:"-"` // hex SHA-256 of trimmed exchanged code
	ExchangedAt       time.Time                        `json:"-"` // timestamp when ExchangedToken was obtained
	// CreatedAt anchors lookup expiry and periodic removal. Keep it unchanged
	// while the retained session is stored and read by the cleanup worker.
	CreatedAt time.Time `json:"created_at"`
}

// AntigravityAuthorizationSessions retains typed authorization proof with a
// stoppable cleanup worker.
type AntigravityAuthorizationSessions struct {
	// authorizationSessionStore owns locking, expiration, and cleanup for these
	// sessions. Construct the enclosing platform store before invoking its methods.
	authorizationSessionStore[AntigravityAuthorizationSession]
}

// NewAntigravityAuthorizationSessions creates empty authorization storage with
// cleanup idle.
// Call Start to begin periodic removal and Stop to join that worker; lookup
// checks creation-time expiry even before Start.
func NewAntigravityAuthorizationSessions() *AntigravityAuthorizationSessions {
	return &AntigravityAuthorizationSessions{
		authorizationSessionStore: newAuthorizationSessionStore(
			AntigravitySessionTTL,
			func(session *AntigravityAuthorizationSession) time.Time { return session.CreatedAt },
		),
	}
}

// Set retains session under sessionID, replacing any previous pointer for that
// identifier. Supply a nonnil session and keep its fields stable while stored
// or borrowed by a reader, including after Stop.
func (s *AntigravityAuthorizationSessions) Set(sessionID string, session *AntigravityAuthorizationSession) {
	s.set(sessionID, session)
}

// Get returns the stored pointer when its creation age is at most
// AntigravitySessionTTL.
// Missing or older entries return nil and false; expired entries remain stored
// until deletion or cleanup, and returned fields remain shared.
func (s *AntigravityAuthorizationSessions) Get(sessionID string) (*AntigravityAuthorizationSession, bool) {
	return s.get(sessionID)
}

// Delete removes sessionID from subsequent lookups and accepts an absent ID.
// Borrowers retain previously returned pointers, so callers coordinate
// exclusive exchange separately from the store's Get and Delete operations.
func (s *AntigravityAuthorizationSessions) Delete(sessionID string) {
	s.delete(sessionID)
}

// Start admits periodic expiry cleanup once for a constructed store.
// Repeated calls leave the running worker unchanged, and calls after Stop
// return with cleanup permanently disabled for this instance.
func (s *AntigravityAuthorizationSessions) Start() {
	s.start()
}

// Stop closes cleanup admission and waits for any admitted worker to exit.
// Repeated calls and calls before Start are supported; Set, Get, and Delete
// remain available after cleanup stops.
func (s *AntigravityAuthorizationSessions) Stop() {
	s.stop()
}
