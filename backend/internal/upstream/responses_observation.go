package upstream

import (
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// responses_observation.go defines the usage, commitment, and disconnect
// evidence returned by Responses readers.
// A Responses reader reports parsed usage and client-write failures
// alongside its parser result. Platform executors transfer these observations
// into attempt results, keeping HTTP commitment, retry commitment, and
// semantic content available as separate decisions for gateway completion.

// ResponsesObservation carries the evidence retained by one Responses output
// reader.
type ResponsesObservation struct {
	// UpstreamResponseModel records the upstream model before client text is
	// restored. Empty means the reader retained no vendor model declaration.
	UpstreamResponseModel string // observed vendor model name
	// HasUsage, Served, HTTPCommitted, RetryCommitted, and ClientDisconnected
	// record independent reader outcomes. Usage and served output can exist
	// alongside a read error; HTTP commitment alone does not close the retry window.
	// HasUsage: usage evidence present. Served: output served by reader.
	// HTTPCommitted: HTTP output started. RetryCommitted: retry window closed.
	// ClientDisconnected: client write failed.
	HasUsage, Served, HTTPCommitted, RetryCommitted, ClientDisconnected bool

	// Usage retains the reader's parsed usage pointer. HasUsage records its
	// consumer-specific presence test, so a nonnil pointer alone is insufficient.
	Usage *wire.ForwardUsage // parsed Responses usage values

	ResponseID string // observed Responses object identifier
	// SearchCount: observed search operations. ImageCount: observed image results.
	ImageFrameCount         int // observed animation frame units
	SearchCount, ImageCount int
	// ImageOutputSizes retains per-image size labels for later completion pricing.
	// The executor can share this slice; copy before modifying retained observations.
	ImageOutputSizes []string // observed image size labels
}
