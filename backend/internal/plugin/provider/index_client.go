package provider

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// index_client.go fetches bounded HTTPS catalog bytes and verifies detached signatures.
// The existing distribution transport supplies proxy routing and HTTPS-only redirect
// checks. Catalogs and signatures share one twenty-second operation budget, while
// independent byte limits apply even when the remote server omits Content-Length.
// No GitHub API or user credential is needed by this runtime client.
//
// Built-in sources always require official verification. Custom pinned sources
// require their configured key, with official classification when that same signature
// also verifies under an official root. Unpinned custom sources may omit a signature;
// an existing but invalid signature never becomes an unsigned fallback. Failed
// verification returns its classification without returning an accepted document.

const (
	// Signature and operation limits bound detached metadata reads and transport
	// lifetime; catalog documents use the byte budget shared with SDK publication.
	maxSignatureBytes = 4 << 10
	indexTimeout      = 20 * time.Second
)

// IndexClient owns a distribution transport and copied official verification roots.
type IndexClient struct {
	client       *http.Client        // bounded HTTPS distribution client
	officialKeys []ed25519.PublicKey // immutable decoded verification roots
}

// NewIndexClient creates a catalog verifier using the distribution proxy policy.
// Empty official keys are allowed for unprovisioned deployments and custom indexes.
// Malformed supplied keys fail construction rather than weakening built-in verification.
func NewIndexClient(proxyURL string, officialKeys []string) (*IndexClient, error) {
	download, err := newDownloader(DownloadOptions{ProxyURL: proxyURL})
	if err != nil {
		return nil, err
	}
	result := &IndexClient{client: download.client}
	result.client.Timeout = indexTimeout
	for _, encoded := range officialKeys {
		key, err := decodeIndexKey(encoded)
		if err != nil {
			return nil, err
		}
		result.officialKeys = append(result.officialKeys, key)
	}
	return result, nil
}

// Fetch authenticates a complete catalog response before returning its original bytes.
// Only an unpinned custom source may treat a signature endpoint's 404 as unsigned.
// Other signature failures preserve invalid classification and never return usable data.
//
// The configured key is an explicit pin and must verify even when another root
// could authenticate the document. Official roots additionally identify trusted
// mirrors after that requirement succeeds. One operation context bounds both reads;
// no partially downloaded or unverified body can escape through a successful result.
func (c *IndexClient) Fetch(ctx context.Context, request plugin.IndexFetchRequest) (plugin.FetchedIndex, error) {
	if !index.HTTPSURL(request.URL) {
		return plugin.FetchedIndex{}, plugin.ErrIndexInvalid
	}
	var pinned ed25519.PublicKey
	if request.PublicKey != "" {
		var err error
		pinned, err = decodeIndexKey(request.PublicKey)
		if err != nil {
			return plugin.FetchedIndex{SignatureStatus: plugin.SignatureInvalid}, plugin.ErrIndexSignatureInvalid.WithCause(err)
		}
	}
	if request.Builtin && len(c.officialKeys) == 0 {
		return plugin.FetchedIndex{SignatureStatus: plugin.SignatureInvalid}, fmt.Errorf("%w: official verification keys are not configured", plugin.ErrIndexSignatureInvalid)
	}
	operation, cancel := context.WithTimeout(ctx, indexTimeout)
	defer cancel()
	data, status, err := c.read(operation, request.URL, index.MaxDocumentBytes)
	if err != nil {
		return plugin.FetchedIndex{}, plugin.ErrDownloadFailed.WithCause(err)
	}
	if status != http.StatusOK {
		return plugin.FetchedIndex{}, fmt.Errorf("%w: index returned HTTP %d", plugin.ErrDownloadFailed, status)
	}
	location, _ := url.Parse(request.URL)
	// Append to both path representations so encoded mirror prefixes and query
	// credentials keep the same meaning when locating the detached signature resource.
	location.Path += ".sig"
	if location.RawPath != "" {
		location.RawPath += ".sig"
	}
	signature, signatureStatus, err := c.read(operation, location.String(), maxSignatureBytes)
	if err != nil {
		return plugin.FetchedIndex{}, plugin.ErrDownloadFailed.WithCause(err)
	}
	if signatureStatus == http.StatusNotFound && !request.Builtin && len(pinned) == 0 {
		return plugin.FetchedIndex{Document: data, SignatureStatus: plugin.SignatureUnsigned}, nil
	}
	if signatureStatus != http.StatusOK {
		if signatureStatus == http.StatusNotFound {
			return plugin.FetchedIndex{SignatureStatus: plugin.SignatureInvalid}, fmt.Errorf("%w: detached signature is missing", plugin.ErrIndexSignatureInvalid)
		}
		return plugin.FetchedIndex{}, fmt.Errorf("%w: signature returned HTTP %d", plugin.ErrDownloadFailed, signatureStatus)
	}
	if len(pinned) > 0 && !request.Builtin {
		if err := index.Verify(data, signature, []ed25519.PublicKey{pinned}); err != nil {
			return plugin.FetchedIndex{SignatureStatus: plugin.SignatureInvalid}, plugin.ErrIndexSignatureInvalid.WithCause(err)
		}
	}
	if err := index.Verify(data, signature, c.officialKeys); err == nil {
		return plugin.FetchedIndex{Document: data, SignatureStatus: plugin.SignatureOfficial}, nil
	}
	if len(pinned) > 0 && !request.Builtin {
		return plugin.FetchedIndex{Document: data, SignatureStatus: plugin.SignatureCustom}, nil
	}
	return plugin.FetchedIndex{SignatureStatus: plugin.SignatureInvalid}, plugin.ErrIndexSignatureInvalid
}

// read returns one bounded response body and status while preserving caller cancellation.
// Every response body is closed before return. Non-success bodies are discarded,
// allowing Fetch to distinguish an absent optional signature from transport failures.
func (c *IndexClient) read(ctx context.Context, location string, maximum int64) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, nil
	}
	if response.ContentLength > maximum {
		return nil, 0, fmt.Errorf("index response exceeds its byte limit")
	}
	// Read one extra byte so chunked or dishonest Content-Length responses cannot
	// bypass the same limit applied to an explicitly declared response size.
	data, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(data)) > maximum {
		return nil, 0, fmt.Errorf("index response exceeds its byte limit")
	}
	return data, response.StatusCode, nil
}

// decodeIndexKey validates one base64 Ed25519 key and returns an independently owned copy.
func decodeIndexKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("index verification key must be a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(slices.Clone(decoded)), nil
}
