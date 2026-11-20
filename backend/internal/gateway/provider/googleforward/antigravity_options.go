package googleforward

import (
	"context"
	"io"
	"net/http"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_options.go supplies bounded error inspection and Antigravity
// model, project, and identity options.
// Antigravity error inspection uses a bounded response prefix separately from
// optional diagnostic logging. The read cap starts at 512 KiB and can grow to
// the requested logging cap, while model mapping, project selection, and
// identity options are delegated to their owning services.

// upstreamErrorBodyReadLimit returns the error inspection cap, starting at 512
// KiB. Configured enabled body logging can enlarge that cap; a nil receiver
// still selects the base read cap.
func (s *Antigravity) upstreamErrorBodyReadLimit() int64 {
	limit := int64(512 << 10)
	if s != nil && s.Options.Configured && s.Options.LogErrorBody && s.Options.LogErrorBodyMaxBytes > int(limit) {
		limit = int64(s.Options.LogErrorBodyMaxBytes)
	}
	return limit
}

// readUpstreamErrorBody returns the available bounded error prefix and leaves
// closure to the caller. Missing response bodies return nil, and source read
// errors are discarded while available bytes remain diagnostic input.
func (s *Antigravity) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	// Retain bytes received before a reading failure because classification
	// and recovery can still inspect that bounded prefix; the caller owns
	// closing the response.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, s.upstreamErrorBodyReadLimit()))
	return body
}

// getLogConfig returns diagnostic body logging and its maximum UTF-8 byte
// count. Unconfigured logging is disabled with a 2048-byte cap; a positive
// configured cap replaces that default.
func (s *Antigravity) getLogConfig() (logBody bool, maxBytes int) {
	maxBytes = 2048
	if !s.Options.Configured {
		return false, maxBytes
	}
	cfg := s.Options
	if cfg.LogErrorBodyMaxBytes > 0 {
		maxBytes = cfg.LogErrorBodyMaxBytes
	}
	return cfg.LogErrorBody, maxBytes
}

// getUpstreamErrorDetail returns bounded UTF-8 body text when diagnostic
// logging is enabled. Disabled logging returns empty, and the selected cap
// comes from getLogConfig before error detail is formatted.
func (s *Antigravity) getUpstreamErrorDetail(body []byte) string {
	logBody, maxBytes := s.getLogConfig()
	if !logBody {
		return ""
	}
	return logredact.TruncateUTF8(string(body), maxBytes)
}

// resolveAntigravityProjectID selects the provider project through the shared
// Antigravity resolver. Missing project data returns ErrProjectIDRequired,
// allowing forwarding to present a local configuration error before upstream
// transport.
func resolveAntigravityProjectID(provider *gatewayprovider.ExecutionProvider) (string, error) {
	return providercore.ResolveAntigravityProjectID(gatewayprovider.ExecutionRecord(provider), antigravity.ErrProjectIDRequired)
}

// getClaudeTransformOptions returns platform transform defaults or current
// gateway identity settings. A missing gateway reader returns the defaults, and
// forwarding callers can enforce identity injection after this option
// selection.
func (s *Antigravity) getClaudeTransformOptions(ctx context.Context) antigravity.TransformOptions {
	opts := antigravity.DefaultTransformOptions()
	if s.Gateway == nil {
		return opts
	}
	opts.EnableIdentityPatch = s.Gateway.IsIdentityPatchEnabled(ctx)
	opts.IdentityPatch = s.Gateway.GetIdentityPatchPrompt(ctx)
	return opts
}
