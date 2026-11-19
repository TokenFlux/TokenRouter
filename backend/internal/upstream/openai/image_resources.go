package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// image_resources.go collects ChatGPT image assets and resolves their inline
// data, download URLs, or authenticated pointers.
// ChatGPT image assets can arrive as file-service or sediment pointers, direct
// download URLs, or inline Base64 text. Collection merges these representations
// by a preferred pointer/URL key or a short encoded prefix, filling missing
// metadata while retaining existing values.
//
// Pointer resolution prefers usable inline data, then a direct URL, then an
// authenticated pointer lookup. Sediment conversation-not-found failures can
// retry while transport failures retry for either pointer form. Download
// authentication is copied for the ChatGPT URL prefix and other targets receive
// a User-Agent; target and redirect policy belongs to the configured
// client/caller. Download reading is truncated to its byte cap, and inline
// decoding has no size or image-format validation here. Error records retain
// bounded bytes and request metadata, while their display message uses query
// sanitization.

// Image resource constants select ChatGPT lookup endpoints, browser identity, and
// bounded error/download capture used when resolving pointer or direct image representations.
const (
	ImageErrorBodyReadLimit     int64 = 512 << 10
	OpenAIChatGPTStartURL             = "https://chatgpt.com/"
	OpenAIChatGPTFilesURL             = "https://chatgpt.com/backend-api/files"
	OpenAIImageBackendUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	OpenAIImageMaxDownloadBytes       = 20 << 20 // 20 MiB per image download
)

// ImagePointerInfo holds one asset representation and optional image prompt
// metadata.
type ImagePointerInfo struct {
	// Pointer, DownloadURL, and B64JSON are alternative representations.
	// ResolveOpenAIImageBytes prefers normalized B64JSON, then DownloadURL, then
	// Pointer lookup, while merge fills only missing representation and metadata fields.
	Pointer     string // ChatGPT asset pointer spelling
	DownloadURL string // direct asset download URL
	B64JSON     string // inline Base64 asset representation
	MimeType    string // reported image media type
	Prompt      string // reported or inherited image prompt
}

// ImageStatusError retains status, captured response evidence, and sanitized
// display text.
type ImageStatusError struct {
	StatusCode      int         // captured backend HTTP status
	Message         string      // sanitized backend display message
	ResponseBody    []byte      // bounded backend response bytes
	ResponseHeaders http.Header // cloned backend response headers
	RequestID       string      // captured upstream request identifier
	// URL retains the raw request spelling and can contain signed query values;
	// callers redact it before expanding diagnostic or serialized error fields.
	URL string
}

// CollectOpenAIImagePointers collects raw pointer spellings and decoded inline
// assets, then merges their metadata. Prompt lookup prefers message metadata,
// then metadata, then revised_prompt; empty input returns nil and discovered
// assets can retain shared string values.
func CollectOpenAIImagePointers(body []byte) []ImagePointerInfo {
	if len(body) == 0 {
		return nil
	}
	prompt := ""
	for _, path := range []string{
		"message.metadata.dalle.prompt",
		"metadata.dalle.prompt",
		"revised_prompt",
	} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			prompt = value
			break
		}
	}
	matches := OpenAIImagePointerMatches(body)
	out := make([]ImagePointerInfo, 0, len(matches))
	for _, pointer := range matches {
		out = append(out, ImagePointerInfo{Pointer: pointer, Prompt: prompt})
	}
	return MergeOpenAIImagePointerInfos(out, CollectOpenAIImageInlineAssets(body, prompt))
}

// OpenAIImagePointerMatches finds literal file-service and sediment pointer
// prefixes followed by ASCII ID characters. Prefixes are searched in that order
// and exact duplicates are removed; this textual scan does not require valid
// JSON.
func OpenAIImagePointerMatches(body []byte) []string {
	raw := string(body)
	matches := make([]string, 0, 4)
	for _, prefix := range []string{"file-service://", "sediment://"} {
		start := 0
		for {
			idx := strings.Index(raw[start:], prefix)
			if idx < 0 {
				break
			}
			idx += start
			end := idx + len(prefix)
			for end < len(raw) {
				ch := raw[end]
				if ch != '-' && ch != '_' &&
					(ch < '0' || ch > '9') &&
					(ch < 'a' || ch > 'z') &&
					(ch < 'A' || ch > 'Z') {
					break
				}
				end++
			}
			matches = append(matches, raw[idx:end])
			start = end
		}
	}
	return DedupeStrings(matches)
}

// MergeOpenAIImagePointerInfos combines next assets with existing values using
// representation identity keys. Empty next returns the existing slice directly.
// Existing entries, including duplicates and keyless values, are retained,
// while keyless next entries are skipped.
//
// Matching next entries fill missing metadata in the first output slot with
// that key, using the latest remembered existing value for merge. New
// identities append in discovery order. Base64 identity uses only a short text
// prefix, so this merge is an asset heuristic rather than full-content
// equality; callers needing distinct encoded images account for possible prefix
// matches.
func MergeOpenAIImagePointerInfos(existing []ImagePointerInfo, next []ImagePointerInfo) []ImagePointerInfo {
	if len(next) == 0 {
		return existing
	}
	seen := make(map[string]ImagePointerInfo, len(existing)+len(next))
	out := make([]ImagePointerInfo, 0, len(existing)+len(next))
	for _, item := range existing {
		if key := item.identityKey(); key != "" {
			seen[key] = item
		}
		out = append(out, item)
	}
	for _, item := range next {
		key := item.identityKey()
		if key == "" {
			continue
		}
		if existingItem, ok := seen[key]; ok {
			merged := MergeOpenAIImagePointerInfo(existingItem, item)
			if merged != existingItem {
				for i := range out {
					if out[i].identityKey() == key {
						out[i] = merged
						break
					}
				}
				seen[key] = merged
			}
			continue
		}
		seen[key] = item
		out = append(out, item)
	}
	return out
}

// identityKey prefers a trimmed pointer, then download URL, then the first
// sixty-four bytes of trimmed Base64 text. Missing representations return empty
// text, and the short encoded key can match different full image payloads.
func (i ImagePointerInfo) identityKey() string {
	switch {
	case strings.TrimSpace(i.Pointer) != "":
		return "pointer:" + strings.TrimSpace(i.Pointer)
	case strings.TrimSpace(i.DownloadURL) != "":
		return "download:" + strings.TrimSpace(i.DownloadURL)
	case strings.TrimSpace(i.B64JSON) != "":
		b64 := strings.TrimSpace(i.B64JSON)
		// The identity shortcut uses a small encoded prefix, bounding key storage
		// while accepting that images with matching initial Base64 text can share this heuristic key.
		if len(b64) > 64 {
			b64 = b64[:64]
		}
		return "b64:" + b64
	default:
		return ""
	}
}

// MergeOpenAIImagePointerInfo fills blank existing representation and metadata
// strings from next. Nonblank existing fields keep precedence and the returned
// value is a copied struct, with immutable strings supplying its contents.
func MergeOpenAIImagePointerInfo(existing, next ImagePointerInfo) ImagePointerInfo {
	merged := existing
	if strings.TrimSpace(merged.Pointer) == "" {
		merged.Pointer = next.Pointer
	}
	if strings.TrimSpace(merged.DownloadURL) == "" {
		merged.DownloadURL = next.DownloadURL
	}
	if strings.TrimSpace(merged.B64JSON) == "" {
		merged.B64JSON = next.B64JSON
	}
	if strings.TrimSpace(merged.MimeType) == "" {
		merged.MimeType = next.MimeType
	}
	if strings.TrimSpace(merged.Prompt) == "" {
		merged.Prompt = next.Prompt
	}
	return merged
}

// ResolveOpenAIImageBytes prefers a usable normalized inline value, then direct
// URL, then pointer lookup and download. Client, context, and permitted request
// headers are caller-owned; inline decode has no image-size or format check and
// an absent representation returns an error.
func ResolveOpenAIImageBytes(
	ctx context.Context,
	client *http.Client,
	headers http.Header,
	conversationID string,
	pointer ImagePointerInfo,
	errorBodyReadLimit int64,
) ([]byte, error) {
	if normalized := NormalizeOpenAIImageBase64(pointer.B64JSON); normalized != "" {
		return base64.StdEncoding.DecodeString(normalized)
	}
	if downloadURL := strings.TrimSpace(pointer.DownloadURL); downloadURL != "" {
		return DownloadOpenAIImageBytes(ctx, client, headers, downloadURL, errorBodyReadLimit)
	}
	if strings.TrimSpace(pointer.Pointer) == "" {
		return nil, fmt.Errorf("image asset is missing pointer, url, and base64 data")
	}
	downloadURL, err := FetchOpenAIImageDownloadURL(ctx, client, headers, conversationID, pointer.Pointer, errorBodyReadLimit)
	if err != nil {
		return nil, err
	}
	return DownloadOpenAIImageBytes(ctx, client, headers, downloadURL, errorBodyReadLimit)
}

// NormalizeOpenAIImageBase64 trims text, removes a data-URI prefix when usable,
// and validates its rebuilt standard-Base64 spelling. It strips supplied
// padding but computes replacement padding from the preceding length, so a
// padded value requiring padding can be rejected; failed validation returns
// empty text.
func NormalizeOpenAIImageBase64(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "data:") {
		if idx := strings.Index(raw, ","); idx >= 0 && idx+1 < len(raw) {
			raw = raw[idx+1:]
		}
	}
	raw = strings.TrimSpace(raw)
	// Replacement padding uses raw length before existing equals signs are
	// removed, so a supplied padded value can fail the subsequent validation.
	raw = strings.TrimRight(raw, "=") + strings.Repeat("=", (4-len(raw)%4)%4)
	if raw == "" {
		return ""
	}
	if _, err := base64.StdEncoding.DecodeString(raw); err != nil {
		return ""
	}
	return raw
}

// CollectOpenAIImageInlineAssets decodes valid JSON and recursively gathers
// plausible asset objects with prompt inheritance. Invalid or empty input
// returns nil, while asset recognition remains a representation heuristic
// rather than image-byte or download-policy validation.
func CollectOpenAIImageInlineAssets(body []byte, fallbackPrompt string) []ImagePointerInfo {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil
	}
	var out []ImagePointerInfo
	WalkOpenAIImageInlineAssets(decoded, strings.TrimSpace(fallbackPrompt), &out)
	return out
}

// WalkOpenAIImageInlineAssets appends plausible asset objects found in maps and
// arrays. The output pointer is required when an asset can be appended, and
// callers own the accumulation slice. Each map prefers revised_prompt,
// image_gen_title, then prompt over its inherited prompt.
//
// Known pointer prefixes, URL hints, or usable normalized Base64 qualify an
// object before its children are visited. A local prompt flows into nested
// maps, while array children keep their parent prompt. Map traversal order is
// not stable, and collected strings retain their supplied representations for
// later merge and resolution rather than becoming validated download authority.
func WalkOpenAIImageInlineAssets(node any, prompt string, out *[]ImagePointerInfo) {
	switch value := node.(type) {
	case map[string]any:
		localPrompt := prompt
		for _, key := range []string{"revised_prompt", "image_gen_title", "prompt"} {
			if v, ok := value[key].(string); ok && strings.TrimSpace(v) != "" {
				localPrompt = strings.TrimSpace(v)
				break
			}
		}
		item := ImagePointerInfo{
			Prompt:      localPrompt,
			Pointer:     FirstNonEmptyString(value["asset_pointer"], value["pointer"]),
			DownloadURL: FirstNonEmptyString(value["download_url"], value["url"], value["image_url"]),
			B64JSON:     FirstNonEmptyString(value["b64_json"], value["base64"], value["image_base64"]),
			MimeType:    FirstNonEmptyString(value["mime_type"], value["mimeType"], value["content_type"]),
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(item.Pointer), "file-service://"),
			strings.HasPrefix(strings.TrimSpace(item.Pointer), "sediment://"),
			IsLikelyOpenAIImageDownloadURL(item.DownloadURL),
			NormalizeOpenAIImageBase64(item.B64JSON) != "":
			*out = append(*out, item)
		}
		for _, child := range value {
			WalkOpenAIImageInlineAssets(child, localPrompt, out)
		}
	case []any:
		for _, child := range value {
			WalkOpenAIImageInlineAssets(child, prompt, out)
		}
	}
}

// FirstNonEmptyString returns the first nonblank string after trimming
// surrounding whitespace. Nonstring values and blank strings are skipped, and
// an absent usable value returns empty text for caller-specific default
// selection.
func FirstNonEmptyString(values ...any) string {
	for _, value := range values {
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// IsLikelyOpenAIImageDownloadURL checks data-image prefixes or HTTP URL text
// with download/image-extension hints. Blank or unrelated strings return false;
// the heuristic does not parse hosts, validate image content, or authorize
// network targets.
func IsLikelyOpenAIImageDownloadURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(raw), "data:image/") {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(raw), "http://") && !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return false
	}
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "/download") ||
		strings.Contains(lower, ".png") ||
		strings.Contains(lower, ".jpg") ||
		strings.Contains(lower, ".jpeg") ||
		strings.Contains(lower, ".webp")
}

// FetchOpenAIImageDownloadURL requests the URL for a file-service or sediment
// pointer with supplied authentication headers. The caller provides nonnil
// context, configured client, validated pointer, and conversation identity
// needed by sediment. Unsupported pointer forms return before sending.
//
// Transport errors retry for either form, while HTTP status errors retry only
// for sediment conversation-not-found evidence. The loop allows eight exchanges
// and waits a fixed 750 milliseconds between eligible failures, with
// cancellation ending the wait. A nonblank successful download_url returns
// immediately; status errors capture bounded response evidence and an exhausted
// retry returns the last available failure.
func FetchOpenAIImageDownloadURL(
	ctx context.Context,
	client *http.Client,
	headers http.Header,
	conversationID string,
	pointer string,
	errorBodyReadLimit int64,
) (string, error) {
	targetURL := ""
	allowConversationRetry := false
	switch {
	case strings.HasPrefix(pointer, "file-service://"):
		fileID := strings.TrimPrefix(pointer, "file-service://")
		targetURL = fmt.Sprintf("%s/%s/download", OpenAIChatGPTFilesURL, fileID)
	case strings.HasPrefix(pointer, "sediment://"):
		attachmentID := strings.TrimPrefix(pointer, "sediment://")
		targetURL = fmt.Sprintf("https://chatgpt.com/backend-api/conversation/%s/attachment/%s/download", conversationID, attachmentID)
		allowConversationRetry = true
	default:
		return "", fmt.Errorf("unsupported image pointer: %s", pointer)
	}

	var lastErr error
	// Retry transport failures for either pointer form and transient conversation
	// lookup failures for sediment, giving attachment availability several bounded lookup attempts.
	for attempt := range 8 {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if reqErr != nil {
			return "", reqErr
		}
		if headers != nil {
			req.Header = CloneHTTPHeader(headers)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			// Cap pointer lookup JSON bodies at 1 MiB before closing the transport stream.
			bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else {
				var result struct {
					DownloadURL string `json:"download_url"` // direct asset download URL
				}
				if decErr := json.Unmarshal(bodyBytes, &result); decErr == nil && strings.TrimSpace(result.DownloadURL) != "" {
					return strings.TrimSpace(result.DownloadURL), nil
				}
				lastErr = fmt.Errorf("fetch image download url failed: missing download_url")
			}
		} else {
			statusErr := NewOpenAIImageStatusError(resp, "fetch image download url failed", errorBodyReadLimit)
			if !allowConversationRetry || !IsOpenAIImageTransientConversationNotFoundError(statusErr) {
				return "", statusErr
			}
			lastErr = statusErr
		}
		if attempt == 7 {
			break
		}
		// Wait between eligible lookup failures with a cancellable timer; stop it
		// on cancellation so pending delay work does not outlive the caller lookup operation.
		timer := time.NewTimer(750 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("fetch image download url failed")
	}
	return "", lastErr
}

// DownloadOpenAIImageBytes reads a caller-approved asset URL through the
// configured HTTP client and closes the response. ChatGPT-prefix URLs receive
// copied supplied headers with image Accept and no Content-Type, while other
// URLs receive a selected User-Agent. Client redirect and target checks remain
// caller responsibilities.
//
// LimitReader returns a truncated prefix when the body exceeds the image byte
// cap; this routine does not reject oversize or sniff image format.
// Non-success responses are converted to ImageStatusError with the configured
// bounded error read, and their original transport body is released by cleanup.
func DownloadOpenAIImageBytes(ctx context.Context, client *http.Client, headers http.Header, downloadURL string, errorBodyReadLimit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(downloadURL, OpenAIChatGPTStartURL) {
		downloadHeaders := CloneHTTPHeader(headers)
		downloadHeaders.Set("Accept", "image/*,*/*;q=0.8")
		downloadHeaders.Del("Content-Type")
		req.Header = downloadHeaders
	} else {
		userAgent := strings.TrimSpace(headers.Get("User-Agent"))
		if userAgent == "" {
			userAgent = OpenAIImageBackendUserAgent
		}
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, NewOpenAIImageStatusError(resp, "download image bytes failed", errorBodyReadLimit)
	}
	// The read cap bounds returned bytes by truncation; it does not inspect an
	// extra byte or prove the upstream image fit within the complete-image budget.
	return io.ReadAll(io.LimitReader(resp.Body, OpenAIImageMaxDownloadBytes))
}

// Error returns the prepared image-backend display message or a status/default
// label. Nil errors use the fixed default; formatting omits the retained
// response body and URL fields, which remain available for explicit
// diagnostics.
func (e *ImageStatusError) Error() string {
	if e == nil {
		return "openai image backend request failed"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("openai image backend request failed: status %d", e.StatusCode)
	}
	return "openai image backend request failed"
}

// NewOpenAIImageStatusError captures available status, cloned headers, request
// URL, and bounded response bytes, closing the response body. Nonpositive read
// limits select ImageErrorBodyReadLimit; nil responses return a plain fallback
// error and display messages sanitize upstream query values.
func NewOpenAIImageStatusError(resp *http.Response, fallback string, errorBodyReadLimit int64) error {
	if resp == nil {
		if strings.TrimSpace(fallback) == "" {
			fallback = "openai image backend request failed"
		}
		return fmt.Errorf("%s", fallback)
	}

	statusCode := resp.StatusCode
	headers := resp.Header.Clone()
	requestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	requestURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		requestURL = resp.Request.URL.String()
	}
	body := []byte(nil)
	if resp.Body != nil {
		if errorBodyReadLimit <= 0 {
			errorBodyReadLimit = ImageErrorBodyReadLimit
		}
		// Capture the available bounded error prefix and close transport ownership;
		// a read failure keeps its partial bytes for diagnostics rather than replacing status evidence.
		body, _ = io.ReadAll(io.LimitReader(resp.Body, errorBodyReadLimit))
		_ = resp.Body.Close()
	}

	message := logredact.SanitizeUpstreamQueries(upstream.ExtractErrorMessage(body))
	if message == "" {
		prefix := strings.TrimSpace(fallback)
		if prefix == "" {
			prefix = "openai image backend request failed"
		}
		message = fmt.Sprintf("%s: status %d", prefix, statusCode)
	}

	return &ImageStatusError{
		StatusCode:      statusCode,
		Message:         message,
		ResponseBody:    body,
		ResponseHeaders: headers,
		RequestID:       requestID,
		URL:             requestURL,
	}
}

// IsOpenAIImageTransientConversationNotFoundError recognizes
// conversation-not-found text in a direct nonnil ImageStatusError with status
// 404. Wrapped or other error types return false; either prepared message or
// extracted captured-body message can supply the marker.
func IsOpenAIImageTransientConversationNotFoundError(err error) bool {
	statusErr, ok := err.(*ImageStatusError)
	if !ok || statusErr == nil || statusErr.StatusCode != http.StatusNotFound {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(statusErr.Message))
	if strings.Contains(msg, "conversation_not_found") {
		return true
	}
	if strings.Contains(msg, "conversation") && strings.Contains(msg, "not found") {
		return true
	}
	bodyMsg := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(statusErr.ResponseBody)))
	if strings.Contains(bodyMsg, "conversation_not_found") {
		return true
	}
	return strings.Contains(bodyMsg, "conversation") && strings.Contains(bodyMsg, "not found")
}

// CloneHTTPHeader copies the header map and each value slice into independently
// mutable storage. Nil input returns a writable empty map, and immutable header
// strings remain the copied values.
func CloneHTTPHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
	return dst
}

// HeaderToMap returns the first value of each nonempty header entry. Empty
// headers produce nil and additional values are omitted, matching the
// single-value map accepted by req request header configuration.
func HeaderToMap(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	result := make(map[string]string, len(header))
	for key, values := range header {
		if len(values) == 0 {
			continue
		}
		result[key] = values[0]
	}
	return result
}

// DedupeStrings returns first occurrences in supplied order using exact string
// equality. Empty input returns nil, a blank string is retained once, and case
// differences remain separate values.
func DedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
