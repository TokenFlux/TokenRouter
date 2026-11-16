package httpx

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/klauspost/compress/zstd"
)

// body.go reads and decompresses request bodies and binds strictly validated
// JSON input.
// Compressed request headers describe transmitted bytes, while JSON consumers
// need decoded bytes. Successful decoding clears Content-Encoding and the
// transmitted Content-Length header, then records the returned byte count on
// the request. The original body is consumed but stays installed, so owners
// that need replay replace that reader with their returned data.
//
// Decoding reads through a fixed sixty-four-MiB limit that can return a
// successful prefix. Completeness and raw-body size remain caller decisions;
// the initial allocation hint bounds preallocation rather than total reading.
// Strict binding separately decodes one JSON value, rejects unknown struct
// fields, requires the trailing stream to end, and invokes Gin validation while
// leaving dynamic map-key validation to its handlers.

const (
	// These capacities bound the initial buffer chosen from ContentLength.
	// Actual copied bytes may grow the buffer, so neither value is a body-size limit.
	requestBodyReadInitCap    = 512
	requestBodyReadMaxInitCap = 1 << 20

	// maxDecompressedBodySize bounds decoded bytes from compression readers;
	// reaching the cap can return a successful prefix for later consumer validation.
	maxDecompressedBodySize = 64 << 20
)

// ReadRequestBodyWithPrealloc returns consumed body bytes, decoding supported encodings.
// Nil requests or bodies return nil without error. Empty and identity encodings
// return raw bytes; zstd, gzip, x-gzip, and zlib-wrapped deflate are decoded.
// Owners supply any raw-body limit, close the original body, and reinstall a
// reader if another consumer needs to read the returned data.
//
// Compressed output may be truncated at sixty-four MiB with no size error, so
// callers validate completeness. After successful decoding, encoding and length
// headers are removed and ContentLength describes the returned decoded bytes.
// Read or decode failure leaves those metadata fields as supplied, although the
// original body may already have been consumed.
func ReadRequestBodyWithPrealloc(req *http.Request) ([]byte, error) {
	body, _, err := ReadRequestBodyWithMaxPrealloc(req, requestBodyReadMaxInitCap)
	return body, err
}

// ReadRequestBodyWithMaxPrealloc reads and decompresses the request body using
// maxInitCap as the upper bound on ContentLength-guided buffer preallocation,
// reporting whether transport decompression ran. Nonpositive maxInitCap values
// fall back to the one-MiB public default.
//
// Authenticated gateway routes pass a larger bounded hint for multi-megabyte
// JSON uploads while public routes keep the conservative default. Header
// cleanup, sixty-four-MiB decompression truncation, and error semantics match
// ReadRequestBodyWithPrealloc.
func ReadRequestBodyWithMaxPrealloc(req *http.Request, maxInitCap int) ([]byte, bool, error) {
	if req == nil || req.Body == nil {
		return nil, false, nil
	}
	if maxInitCap <= 0 {
		maxInitCap = requestBodyReadMaxInitCap
	}
	if maxInitCap < requestBodyReadInitCap {
		maxInitCap = requestBodyReadInitCap
	}

	// Treat ContentLength as an allocation hint within caller-supplied initial bounds.
	// Large declared lengths grow storage only as io.Copy actually consumes bytes.
	capHint := requestBodyReadInitCap
	if req.ContentLength > 0 {
		switch {
		case req.ContentLength < int64(requestBodyReadInitCap):
			capHint = requestBodyReadInitCap
		case req.ContentLength > int64(maxInitCap):
			capHint = maxInitCap
		default:
			capHint = int(req.ContentLength)
		}
	}

	buf := bytes.NewBuffer(make([]byte, 0, capHint))
	if _, err := io.Copy(buf, req.Body); err != nil {
		return nil, false, err
	}
	raw := buf.Bytes()

	enc := strings.ToLower(strings.TrimSpace(req.Header.Get("Content-Encoding")))
	if enc == "" || enc == "identity" {
		return raw, false, nil
	}

	decoded, err := decompressRequestBody(enc, raw)
	if err != nil {
		return nil, true, fmt.Errorf("decode Content-Encoding %q: %w", enc, err)
	}

	// Metadata now describes returned decoded bytes. Clear the transmitted
	// encoding and length so a replay owner does not decompress or size them again.
	req.Header.Del("Content-Encoding")
	req.Header.Del("Content-Length")
	req.ContentLength = int64(len(decoded))

	return decoded, true, nil
}

// decompressRequestBody decodes buffered bytes in the named supported compression format.
// Its readers are owned and closed here; gzip and zlib close errors are discarded
// after the read result is chosen. Unsupported names return an error, and
// decoded output can stop successfully at maxDecompressedBodySize without proving
// the whole compressed stream was consumed.
func decompressRequestBody(encoding string, raw []byte) ([]byte, error) {
	// LimitReader supplies EOF at the cap without reading an extra byte to
	// detect overflow. Every compression branch therefore exposes the same prefix behavior.
	switch encoding {
	case "zstd":
		dec, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		return io.ReadAll(io.LimitReader(dec, maxDecompressedBodySize))
	case "gzip", "x-gzip":
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		// The buffered read result determines decoding success. Close releases
		// decoder state afterward, and its error is discarded instead of replacing that result.
		defer func() {
			_ = gr.Close()
		}()
		return io.ReadAll(io.LimitReader(gr, maxDecompressedBodySize))
	case "deflate":
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		// Retain ReadAll's returned data and error as the decoding outcome.
		// Deferred zlib closure cleans up the reader with its own error discarded.
		defer func() {
			_ = zr.Close()
		}()
		return io.ReadAll(io.LimitReader(zr, maxDecompressedBodySize))
	default:
		return nil, errors.New("unsupported Content-Encoding")
	}
}

// BindJSONStrict decodes one JSON value and applies Gin validation to its target.
// Supply a populated request body and a writable target accepted by encoding/json.
// Unknown struct fields and trailing input are rejected; the target determines
// the value's shape, while handlers validate dynamic maps such as credentials.
func BindJSONStrict(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	// A second decode must reach EOF, so another value or malformed suffix
	// cannot be silently accepted after the target's first successful JSON decode.
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("request must contain one JSON object")
	}
	return binding.Validator.ValidateStruct(target)
}
