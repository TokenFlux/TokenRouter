package plugin

import (
	"context"
	"encoding/json"
)

// indexes.go defines the network boundary and trust roots used by catalog management.
// Fetchers authenticate original response bytes before the core validates the
// document schema and replaces a cache. The built-in source requires a project-owned
// verification key; an empty key list never silently permits unsigned official data.

const (
	// Signature classifications distinguish accepted trust policies from failed refresh attempts without treating cached schema acceptance as signature verification.
	SignatureUnknown  = "unknown"  // no successful signature observation
	SignatureOfficial = "official" // verified by an official root
	SignatureCustom   = "custom"   // verified by the configured key
	SignatureUnsigned = "unsigned" // explicitly unpinned source without signature
	SignatureInvalid  = "invalid"  // required verification did not succeed
)

// OfficialIndexKeys contains project-provisioned base64 Ed25519 public keys for rotation.
// The project owner supplies these keys alongside the index repository's signing
// secret. The private key remains outside this repository; fixture keys belong
// only to injected test clients and never enter this production verification list.
var OfficialIndexKeys = []string{
	"zrMvbppI8zA0Xkh3T04YKI0q78GowxyM3Pogcxq3qh4=",
}

// IndexFetchRequest supplies the transport-only authority needed to retrieve one source.
type IndexFetchRequest struct {
	URL       string // configured HTTPS catalog URL
	PublicKey string // optional pinned base64 public key
	Builtin   bool   // official roots are mandatory
}

// FetchedIndex contains original downloaded bytes and the observed trust classification.
type FetchedIndex struct {
	// Document belongs to the result; callers validate its schema and persist it
	// only after Fetch succeeds, preserving raw bytes until signature verification finishes.
	Document json.RawMessage
	// SignatureStatus may describe invalid verification alongside an error, allowing
	// callers to record diagnostics without replacing their last accepted document.
	SignatureStatus string
}

// IndexFetcher retrieves bounded original bytes under the selected source trust policy.
type IndexFetcher interface {
	// Fetch retrieves a catalog and its detached signature without parsing contribution semantics.
	// A nonempty classification can accompany failure. Callers must retain their old
	// document on any error and validate successful bytes before accepting a replacement.
	Fetch(context.Context, IndexFetchRequest) (FetchedIndex, error)
}

// ReleaseResolver selects an accepted indexed snapshot for a specific reviewed version.
type ReleaseResolver interface {
	// Resolve returns independently owned release state after source and compatibility checks.
	// The caller still validates permission acceptance under the plugin mutation lock.
	// Explicit testing options may waive version bounds and source ownership;
	// disabled, withdrawn, missing, or unsupported selections still fail without downloading archives.
	Resolve(context.Context, InstallInput) (VersionSnapshot, error)
}
