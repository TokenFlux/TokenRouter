package plugin

// capabilities.go describes the host collaboration data accepted by plugin
// capability adapters. Administrative audit records contain bounded business
// identifiers and outcomes; caller grants, configuration secrets, and message bodies
// are excluded before the adapter hands an immutable observation to the audit queue.

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
