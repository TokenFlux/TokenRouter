package antigravity

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// response_transformer.go converts wrapped or direct Gemini responses to
// Claude responses with generated identifiers.
// Antigravity response conversion accepts wrapped or direct Gemini JSON and
// delegates content/usage mapping to the shared Anthropic processor. Absent
// inner candidates select the direct shape when an otherwise decoded envelope
// is empty, and processor diagnostics are logged beside the response.
//
// Runtime ID callbacks use cryptographic bytes mapped into an alphanumeric
// alphabet with a small remainder bias. Time/counter branches remain defensive
// temporary-ID logic. The current crypto/rand runtime terminates on read
// failure before returning an error, so those branches are not a live recovery
// guarantee or authentication mechanism.

// fallbackCounter supplies atomic per-call variation to the defensive
// time-seeded identifier branch when a random reader returns an error to that
// branch.
var fallbackCounter uint64

// NonStreamingProcessor adds platform diagnostic logging to shared buffered
// Gemini-to-Anthropic conversion.
type NonStreamingProcessor struct {
	// The embedded processor retains content and diagnostics across calls; use a
	// fresh instance for each answer and serialize access.
	*bridge.GeminiToAnthropicResponseProcessor
}

// TransformGeminiToClaude returns Claude JSON and usage using originalModel as
// the client model identity. It accepts a v1internal envelope or direct Gemini
// data; undecodable input and output serialization failures return errors.
//
// An envelope decoding error selects direct decoding. A successful envelope
// with no inner candidates also selects the direct shape, because unknown
// direct fields can otherwise decode into an empty envelope. Both direct paths
// move response ID and model-version observations beside the selected response
// before the shared processor maps its content and usage. Processor
// diagnostics are logged.
func TransformGeminiToClaude(geminiResp []byte, originalModel string) ([]byte, *ClaudeUsage, error) {
	var v1Resp V1InternalResponse
	if err := json.Unmarshal(geminiResp, &v1Resp); err != nil {
		// Try direct decoding after an envelope error so an upstream returning
		// unwrapped generation data can still enter the same shared response
		// conversion processor.
		var directResp GeminiResponse
		if err2 := json.Unmarshal(geminiResp, &directResp); err2 != nil {
			return nil, nil, fmt.Errorf("parse gemini response: %w", err)
		}
		v1Resp.Response = directResp
		v1Resp.ResponseID = directResp.ResponseID
		v1Resp.ModelVersion = directResp.ModelVersion
	} else if len(v1Resp.Response.Candidates) == 0 {
		// A direct object can decode into an empty envelope without error because
		// unknown fields are ignored; inspect that shape when inner candidates are
		// absent.
		var directResp GeminiResponse
		if err2 := json.Unmarshal(geminiResp, &directResp); err2 != nil {
			return nil, nil, fmt.Errorf("parse gemini response as direct: %w", err2)
		}
		v1Resp.Response = directResp
		v1Resp.ResponseID = directResp.ResponseID
		v1Resp.ModelVersion = directResp.ModelVersion
	}

	processor := NewNonStreamingProcessor()
	claudeResp := processor.Process(&v1Resp.Response, v1Resp.ResponseID, originalModel)

	respBytes, err := json.Marshal(claudeResp)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal claude response: %w", err)
	}

	return respBytes, &claudeResp.Usage, nil
}

// generateRandomID returns twelve alphanumeric characters using
// remainder-mapped random bytes. The defensive time/counter path lies outside
// current crypto/rand returned-error behavior; these temporary IDs have
// possible collisions and serve response identity rather than authentication.
func generateRandomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// Allocate the fixed temporary-ID width before mapping random bytes into the
	// protocol alphabet.
	id := make([]byte, 12)
	randBytes := make([]byte, 12)
	if _, err := rand.Read(randBytes); err != nil {
		// Retain this defensive generator for a returned reader error; the current
		// Go runtime terminates on entropy failure before returning one to this
		// branch.
		cnt := atomic.AddUint64(&fallbackCounter, 1)
		seed := uint64(time.Now().UnixNano()) ^ cnt
		seed ^= uint64(len(err.Error())) << 32
		for i := range id {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			id[i] = chars[int(seed)%len(chars)]
		}
		return string(id)
	}
	// Map fixed random bytes into the temporary response alphabet by remainder,
	// retaining a small bias and possible collisions rather than establishing
	// unique identity.
	for i, b := range randBytes {
		id[i] = chars[int(b)%len(chars)]
	}
	return string(id)
}

// generateAnthropicMsgID returns msg_01 followed by twenty-two alphanumeric
// characters from random bytes. Its defensive helper fallback shares
// temporary-ID behavior; callers use the value for message identity rather
// than authentication.
func generateAnthropicMsgID() string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// Message IDs reserve twenty-two characters after the Anthropic prefix.
	const idLen = 22
	randomBytes := make([]byte, idLen)
	// Retain helper-based temporary IDs for a returned entropy error; current
	// crypto/rand runtime failure terminates before this defensive branch can
	// execute.
	if _, err := rand.Read(randomBytes); err != nil {
		return "msg_01" + generateRandomID() + generateRandomID()[:10]
	}
	b := make([]byte, idLen)
	for i := range b {
		b[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return "msg_01" + string(b)
}

// NewNonStreamingProcessor constructs one buffered-answer converter using
// platform ID callbacks. Use a fresh instance per answer, and serialize
// processing while returned content is read or serialized because content
// shares processor storage.
func NewNonStreamingProcessor() *NonStreamingProcessor {
	return &NonStreamingProcessor{bridge.NewGeminiToAnthropicResponseProcessor(geminiConversionRuntime())}
}

// Process converts a nonnil response and drains diagnostics into platform
// logs. Use one processor per answer; returned content shares processor
// storage and tool arguments retain decoded references, which callers keep
// stable through serialization.
func (p *NonStreamingProcessor) Process(response *GeminiResponse, responseID, model string) *ClaudeResponse {
	result := p.GeminiToAnthropicResponseProcessor.Process(response, responseID, model)
	for _, message := range p.TakeDiagnostics() {
		logger.LegacyPrintf("upstream.antigravity", "%s", message)
	}
	return result
}

// geminiConversionRuntime supplies platform response and tool ID callbacks to
// shared conversion.
func geminiConversionRuntime() bridge.GeminiConversionRuntime {
	return bridge.GeminiConversionRuntime{RandomID: generateRandomID, MessageID: generateAnthropicMsgID}
}
