package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// body.go reads request bodies and adapts lenient JSON normalization errors for
// HTTP handlers. Client JSON can contain raw control bytes that a strict parser
// rejects inside strings. Gateway adapters delegate that compatibility pass to
// the OpenAI normalizer, record decoded payload telemetry, and translate its
// expanded-size limit into the HTTP error type consumed by endpoint handlers.

// gatewayRequestBodyMaxInitCap bounds initial buffer preallocation for
// authenticated gateway JSON uploads so multi-megabyte histories avoid repeated
// growth copies without trusting arbitrary Content-Length headers.
const gatewayRequestBodyMaxInitCap = 8 << 20

// ReadLenientJSONRequestBodyWithPrealloc reads and decodes the body before
// escaping raw control bytes inside JSON strings. maxNormalizedBytes limits the
// normalized result; read and decoding failures stop processing before normalization,
// while successful reads record decoded payload bytes and decode CPU time.
func ReadLenientJSONRequestBodyWithPrealloc(req *http.Request, maxNormalizedBytes int64) ([]byte, error) {
	maxInitCap := gatewayRequestBodyMaxInitCap
	if maxNormalizedBytes > 0 && maxNormalizedBytes < int64(maxInitCap) {
		maxInitCap = int(maxNormalizedBytes)
	}
	startedAt := time.Now()
	body, compressed, err := httpx.ReadRequestBodyWithMaxPrealloc(req, maxInitCap)
	if err != nil {
		return nil, err
	}
	normalized, err := NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
	if err != nil {
		return nil, err
	}
	if req != nil {
		timing.ObserveBodyDecode(req.Context(), int64(len(normalized)), compressed, time.Since(startedAt))
	}
	return normalized, nil
}

// NormalizeLenientJSONRequestBody normalizes client JSON and translates size failures
// to http.MaxBytesError with the same limit. A nonpositive limit selects the
// protocol normalizer's 64 MiB default. Output may share body storage, and malformed
// JSON structure remains for the caller's validator to reject.
func NormalizeLenientJSONRequestBody(body []byte, limit int64) ([]byte, error) {
	result, err := openai.NormalizeLenientJSONRequestBody(body, limit)
	// HTTP handlers recognize MaxBytesError to choose their body-too-large response.
	// Keep the protocol normalizer's limit so expansion failures report the same configured bound.
	if exceeded, ok := errors.AsType[*openai.BodyLimitError](err); ok {
		return nil, &http.MaxBytesError{Limit: exceeded.Limit}
	}
	return result, err
}

// ReadRequestBodyWithPrealloc delegates body reading and decompression to httpx
// with the authenticated gateway preallocation bound and records decoded payload
// telemetry. A nil request or body returns nil without error. The reader consumes
// the body; successful decoding updates encoding and length metadata, while callers
// that need to reread input install a replacement body themselves.
// httpx can truncate decoded bytes at 64 MiB without a size error. Callers
// validate completeness, supply any limit on encoded or unencoded input, and
// close the original body after its use.
func ReadRequestBodyWithPrealloc(req *http.Request) ([]byte, error) {
	startedAt := time.Now()
	body, compressed, err := httpx.ReadRequestBodyWithMaxPrealloc(req, gatewayRequestBodyMaxInitCap)
	if err != nil {
		return nil, err
	}
	if req != nil {
		timing.ObserveBodyDecode(req.Context(), int64(len(body)), compressed, time.Since(startedAt))
	}
	return body, nil
}
