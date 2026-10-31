package plugin

import "context"

// capabilities.go describes the host collaboration data accepted by plugin
// capability adapters. Administrative audit records contain bounded business
// identifiers and outcomes; caller grants, configuration secrets, and message bodies
// are excluded before the adapter hands an immutable observation to the audit queue.
// CAPTCHA adapters retain provider selection and proof verification in identity.

// CapabilityAudit records one attempted write RPC, including denied and replayed calls.
type CapabilityAudit struct {
	PluginID    string // authenticated plugin installation identity
	Version     string // executing plugin release version
	RPC         string // attempted host method name
	ActorUserID int64  // verified optional grant subject
	UserID      int64  // requested target account identifier
	Key         string // bounded operation replay key
	Replayed    bool   // original operation result was reused
	Amount      string // bounded requested amount text
	RedeemType  string // bounded requested credential entitlement type
	Quantity    int64  // attempted credential issuance count
	PlanID      int64  // optional requested plan identifier
	Days        int32  // optional requested entitlement duration
	Reason      string // bounded operation explanation text
	ErrorReason string // safe protocol failure classification
	StatusCode  int    // HTTP-equivalent audit outcome status
	LatencyMs   int64  // milliseconds
}

// CapabilityAuditor accepts completed write observations without owning financial persistence.
type CapabilityAuditor interface {
	// RecordCapability submits an immutable best-effort observation to the existing audit queue.
	// Queue admission may drop records under its existing policy. Financial and
	// notification ledger entries remain the authoritative evidence of committed effects.
	RecordCapability(CapabilityAudit)
}

// CaptchaResult separates an enabled challenge from its verification decision.
type CaptchaResult struct {
	Enabled bool // site challenge provider enabled
	Success bool // accepted proof or disabled provider
}

// CaptchaVerifier checks opaque browser proofs through the existing identity services.
type CaptchaVerifier interface {
	// VerifyCaptcha returns the selected provider state and proof acceptance.
	// Disabled sites return success. Configuration and service failures return errors;
	// an enabled provider rejecting the submitted proof returns Success false.
	VerifyCaptcha(context.Context, string, string) (CaptchaResult, error)
}
