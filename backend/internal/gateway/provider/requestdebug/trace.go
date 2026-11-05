package requestdebug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// trace.go captures gateway request identity summaries and optional header and
// body snapshots for debugging.
// Gateway diagnostics compare inbound and outbound Messages requests using
// compact identity lines or complete file snapshots. Authorization and
// x-api-key values are redacted; other header values, request bodies, and selected
// metadata remain available for inspecting protocol preparation and client
// fingerprints.

// debugGatewayBodyDefaultFilename names the append log selected by a true-like path setting or an existing directory.
const debugGatewayBodyDefaultFilename = "gateway_debug.log"

// Trace holds the application-owned diagnostic log and mimic logging switch.
// Drain requests and background users before Close, because snapshots borrow the
// open file while closeOnce records a single close result.
type Trace struct {
	// file, closeOnce, and closeErr share the log lifetime: open assigns file,
	// Close records its result once, and Snapshot borrows it until users drain.
	file      *os.File  // opened append log
	mimic     bool      // compact mimic logging
	closeOnce sync.Once // one close attempt
	closeErr  error     // retained close result
}

// New configures request diagnostics from the path and mimic settings. A blank
// path disables file snapshots; opening failures are logged and leave file
// snapshots disabled while the mimic switch remains usable.
func New(path string, mimic bool) *Trace {
	trace := &Trace{mimic: mimic}
	if path = strings.TrimSpace(path); path != "" {
		trace.open(path)
	}
	return trace
}

// Close releases the diagnostic file and returns its retained close error. Call
// it after all capture users have finished; repeated calls share the result of
// the first close attempt.
func (s *Trace) Close() error {
	s.closeOnce.Do(func() {
		if s.file != nil {
			s.closeErr = s.file.Close()
		}
	})
	return s.closeErr
}

// Capture returns a compact identity line when retention or configured mimic
// logging requests it. A nil Trace can still produce retained diagnostics, and
// enabled mimic logging also writes nonempty lines through LegacyPrintf.
func (s *Trace) Capture(req *http.Request, body []byte, target *gatewayprovider.ExecutionProvider, tokenType string, mimic, retain bool) string {
	if !retain && (s == nil || !s.mimic) {
		return ""
	}
	line := CaptureLine(req, body, target, tokenType, mimic)
	if s != nil && s.mimic && line != "" {
		logging.LegacyPrintf("service.gateway", "[ClaudeMimicDebug] %s", line)
	}
	return line
}

// redactAuthHeaderValue replaces nonempty credentials with a redaction marker.
// Bearer values keep a recognizable scheme for comparing authentication modes;
// blank values remain blank after whitespace trimming.
func redactAuthHeaderValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Bearer diagnostics retain the authentication scheme so request comparisons
	// can distinguish token authentication while concealing the token value.
	if strings.HasPrefix(strings.ToLower(v), "bearer ") {
		return "Bearer [redacted]"
	}
	return "[redacted]"
}

// safeHeaderValueForLog trims values and redacts Authorization and x-api-key
// headers. Names are matched after trimming and lowercasing; all other header
// values retain their content after surrounding whitespace is removed.
func safeHeaderValueForLog(key string, v string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "authorization", "x-api-key":
		return redactAuthHeaderValue(v)
	default:
		return strings.TrimSpace(v)
	}
}

// extractSystemPreviewFromBody selects a system string or the first nonblank
// text block. Missing, empty, or differently shaped system values produce an
// empty preview, and returned text keeps its original whitespace.
func extractSystemPreviewFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sys := gjson.GetBytes(body, "system")
	if !sys.Exists() {
		return ""
	}

	switch {
	case sys.IsArray():
		for _, item := range sys.Array() {
			if !item.IsObject() {
				continue
			}
			if strings.EqualFold(item.Get("type").String(), "text") {
				if t := item.Get("text").String(); strings.TrimSpace(t) != "" {
					return t
				}
			}
		}
		return ""
	case sys.Type == gjson.String:
		return sys.String()
	default:
		return ""
	}
}

// CaptureLine builds one request identity line with selected fingerprint
// headers and a system preview. A nil request returns an empty string; callers
// supply a request with a URL, and the preview is trimmed and bounded before
// line escaping.
func CaptureLine(req *http.Request, body []byte, provider *gatewayprovider.ExecutionProvider, tokenType string, mimicClaudeCode bool) string {
	if req == nil {
		return ""
	}

	// Capture fingerprint headers with redacted authentication values.
	interesting := []string{
		"user-agent",
		"x-app",
		"anthropic-dangerous-direct-browser-access",
		"anthropic-version",
		"anthropic-beta",
		"x-stainless-lang",
		"x-stainless-package-version",
		"x-stainless-os",
		"x-stainless-arch",
		"x-stainless-runtime",
		"x-stainless-runtime-version",
		"x-stainless-retry-count",
		"x-stainless-timeout",
		"authorization",
		"x-api-key",
		"content-type",
		"accept",
		"x-stainless-helper-method",
	}

	h := make([]string, 0, len(interesting))
	for _, k := range interesting {
		if v := req.Header.Get(k); v != "" {
			h = append(h, fmt.Sprintf("%s=%q", k, safeHeaderValueForLog(k, v)))
		}
	}

	metaUserID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())
	sysPreview := strings.TrimSpace(extractSystemPreviewFromBody(body))

	// Bound the preview before escaping so full system prompts do not dominate the
	// compact identity line; the bound counts bytes.
	if len(sysPreview) > 300 {
		sysPreview = sysPreview[:300] + "..."
	}
	sysPreview = strings.ReplaceAll(sysPreview, "\n", "\\n")
	sysPreview = strings.ReplaceAll(sysPreview, "\r", "\\r")

	aid := int64(0)
	aname := ""
	if provider != nil {
		aid = provider.Record.ID
		aname = provider.Record.Name
	}

	return fmt.Sprintf(
		"url=%s provider=%d(%s) tokenType=%s mimic=%t meta.user_id=%q system.preview=%q headers={%s}",
		req.URL.String(),
		aid,
		aname,
		tokenType,
		mimicClaudeCode,
		metaUserID,
		sysPreview,
		strings.Join(h, " "),
	)
}

// open selects an append log from a path, directory, or true-like debug
// setting. Directory creation and file-opening failures are logged and
// suppressed so diagnostic setup leaves forwarding available with file
// snapshots disabled.
func (s *Trace) open(path string) {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "1", "true", "yes", "on":
		path = debugGatewayBodyDefaultFilename
	}

	// A directory setting identifies the capture location, so append the
	// conventional filename before creating parents and opening the log.
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, debugGatewayBodyDefaultFilename)
	}

	// Prepare nested capture paths before opening the append file; a creation
	// failure disables snapshots and is reported through ordinary logging.
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Error("failed to create gateway debug log directory", "dir", dir, "error", err)
			return
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Error("failed to open gateway debug log file", "path", path, "error", err)
		return
	}
	s.file = f
	slog.Info("gateway debug logging enabled", "path", path)
}

// Snapshot appends a complete request capture to the configured log after
// building context, headers, and body in memory. An unopened file skips
// capture, malformed JSON is written as supplied, and write failures are
// discarded. Callers keep the Trace open until snapshots finish. Authorization
// and x-api-key values are masked; other headers, body bytes, and extra context
// retain the caller-supplied data.
func (s *Trace) Snapshot(tag string, headers http.Header, body []byte, extra map[string]string) {
	f := s.file
	if f == nil {
		return
	}

	var buf strings.Builder
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(&buf, "\n========== [%s] %s ==========\n", ts, tag)

	// Sort context keys for repeatable output.
	if len(extra) > 0 {
		fmt.Fprint(&buf, "--- context ---\n")
		extraKeys := make([]string, 0, len(extra))
		for k := range extra {
			extraKeys = append(extraKeys, k)
		}
		sort.Strings(extraKeys)
		for _, k := range extraKeys {
			fmt.Fprintf(&buf, "  %s: %s\n", k, extra[k])
		}
	}

	// Claude CLI ordering helps compare header placement with packet captures;
	// mask Authorization and x-api-key values while keeping other supplied headers
	// available for comparison.
	fmt.Fprint(&buf, "--- headers ---\n")
	for _, k := range claude.SortHeadersByWireOrder(headers) {
		for _, v := range headers[k] {
			fmt.Fprintf(&buf, "  %s: %s\n", k, safeHeaderValueForLog(k, v))
		}
	}

	// Pretty-print the full JSON body for comparison.
	fmt.Fprint(&buf, "--- body ---\n")
	if len(body) == 0 {
		fmt.Fprint(&buf, "  (empty)\n")
	} else {
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "  ", "  ") == nil {
			fmt.Fprintf(&buf, "  %s\n", pretty.Bytes())
		} else {
			// Keep malformed or non-JSON payload bytes available for comparison because
			// formatting failure is itself evidence about the request being forwarded.
			fmt.Fprintf(&buf, "  %s\n", body)
		}
	}

	// Submit the assembled capture as one file write; discarded write errors keep
	// optional diagnostics from becoming request-forwarding failures.
	_, _ = f.WriteString(buf.String())
}
