package moderation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// audit.go chunks text and media units, calls external moderation endpoints
// through configured proxies and prioritized API keys, and aggregates category
// scores against configured thresholds. Multi-chunk text batches fall back to
// single-item requests only when an upstream endpoint rejects array inputs,
// preserving transient error status for key-health tracking without multiplying
// outbound traffic.
//
// Proxy URLs resolve through short-lived cache entries and never fall back to
// direct egress when proxy lookup fails, preventing accidental origin IP
// exposure. Confirmed category hits remain authoritative even when sibling
// audit chunks fail, whereas total transport failure records incomplete audit
// metadata under the active fail-open policy.

// ContentModerationFailedUnit associates public failure text with its audit
// unit and original image review position.
type ContentModerationFailedUnit struct {
	Type  string `json:"type"`  // text, image, or request
	Index int    `json:"index"` // selected audit unit position
	// SourceIndex maps image failures to the original review item. Text chunk and
	// request-level failures keep zero because their unit constructors do not
	// assign a separate review position.
	SourceIndex int    `json:"source_index"` // original image review position
	Error       string `json:"error"`        // public failed unit summary
}

// contentModerationAuditResult aggregates successful score maxima, failed
// units, and flagged image positions for one audit.
type contentModerationAuditResult struct {
	// CategoryScores, FailedUnits, FlaggedImageIndexes, and SuccessfulUnits are
	// mutated by one merger consumer. Image workers send unit values through the
	// result channel; successful maxima remain usable when failures occur.
	CategoryScores map[string]float64 // successful category score maxima
	// public unit failure observations
	FailedUnits         []ContentModerationFailedUnit
	FlaggedImageIndexes []int // selected audit image indexes
	TextUnitCount       int   // planned text chunk count
	ImageUnitCount      int   // planned selected image count
	SuccessfulUnits     int   // successful merged unit count
	AuditComplete       bool  // no failed unit observed
}

// contentModerationUnitResult links one returned API outcome or error with its
// original audit unit identity.
type contentModerationUnitResult struct {
	unitType    string // text or image kind
	index       int    // selected audit unit position
	sourceIndex int    // original image review position
	// result and err describe a completed unit before its value is sent to the
	// merger. Workers keep the result and its score map stable after sending; the
	// merger copies category maxima into its own aggregate map.
	// successful returned score values
	result *moderationAPIResult
	err    error // unit attempt failure result
}

// contentModerationAPIError retains a received failure status and bounded
// provider message for diagnostic handling.
type contentModerationAPIError struct {
	StatusCode int    // received failure HTTP status
	Message    string // bounded captured provider diagnostic
}

// moderationProxyURLCacheEntry is one immutable stored-proxy resolution
// published through the service atomic pointer.
type moderationProxyURLCacheEntry struct {
	proxyID   int64     // resolved stored proxy identifier
	url       string    // resolved outbound proxy URL
	expiresAt time.Time // process-local cache expiry instant
}

// moderationAPIRequest is the JSON model and input shape sent to the external
// moderation transport.
type moderationAPIRequest struct {
	Model string `json:"model"` // configured external audit model
	Input any    `json:"input"` // scalar, batch, or multimodal input
}

// moderationAPIInputPart represents one text or image-URL part in a combined
// administrator audit request.
type moderationAPIInputPart struct {
	// Type selects the serialized part form. Text is used for text parts and
	// ImageURL for image parts; omitted companions use omitempty in the request
	// encoding.
	Type string `json:"type"`           // text or image_url label
	Text string `json:"text,omitempty"` // selected audit text value
	// nested audit image reference
	ImageURL *moderationAPIImageURLRef `json:"image_url,omitempty"`
}

// moderationAPIImageURLRef is the nested image-reference shape serialized for
// the moderation request.
type moderationAPIImageURLRef struct {
	URL string `json:"url"` // literal audit image reference
}

// moderationAPIResponse decodes the ordered result list returned by the
// external moderation endpoint.
type moderationAPIResponse struct {
	// ordered external audit outcomes
	Results []moderationAPIResult `json:"results"`
}

// moderationAPIResult decodes provider score observations that are evaluated
// against local configured thresholds.
type moderationAPIResult struct {
	// Flagged is decoded for compatibility with provider replies. Local admission
	// and administrator probe flagging use CategoryScores and configured
	// known-category thresholds instead of this provider boolean.
	// provider supplied flag observation
	Flagged bool `json:"flagged"`
	// provider supplied category scores
	CategoryScores map[string]float64 `json:"category_scores"`
}

// TestAPIKeys probes supplied credentials or the saved pool with temporary
// endpoint, model, timeout, and proxy overrides. An omitted proxy uses saved
// selection and a nonpositive override selects direct transport. Blank test
// content sends hello; saved credentials with real audit content select one
// scheduled unfrozen key, while explicit credentials or a health-only request
// probe their whole selected list.
//
// Individual call failures update process-local key health and are returned as
// item statuses, rather than failing the overall probe. The first successful
// result supplies the optional displayed audit evaluation. Validation and
// configuration read errors still fail the operation before probing, and input
// overrides are not persisted.
func (s *ContentModerationService) TestAPIKeys(ctx context.Context, input TestContentModerationAPIKeysInput) (*TestContentModerationAPIKeysResult, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	keys := normalizeModerationAPIKeys(input.APIKeys)
	configured := false
	if len(keys) == 0 {
		keys = cfg.apiKeys()
		configured = true
	}
	if strings.TrimSpace(input.BaseURL) != "" {
		cfg.BaseURL = input.BaseURL
	}
	if strings.TrimSpace(input.Model) != "" {
		cfg.Model = input.Model
	}
	if input.TimeoutMS > 0 {
		cfg.TimeoutMS = input.TimeoutMS
	}
	if input.ProxyID != nil {
		if *input.ProxyID > 0 {
			proxyID := *input.ProxyID
			cfg.ProxyID = &proxyID
		} else {
			cfg.ProxyID = nil
		}
	}
	cfg.normalize()
	testInput, imageCount, err := buildModerationTestInput(input.Prompt, input.Images)
	if err != nil {
		return nil, err
	}
	auditOnly := contentModerationTestHasAuditInput(input.Prompt, input.Images)
	if configured && auditOnly {
		key, ok := s.nextUsableAPIKey(cfg)
		if !ok {
			return &TestContentModerationAPIKeysResult{
				Items:      s.apiKeyStatuses(cfg),
				ImageCount: imageCount,
			}, nil
		}
		keys = []string{key}
	}
	if len(keys) == 0 {
		return &TestContentModerationAPIKeysResult{Items: []ContentModerationAPIKeyStatus{}, ImageCount: imageCount}, nil
	}
	items := make([]ContentModerationAPIKeyStatus, 0, len(keys))
	var auditResult *ContentModerationTestAuditResult
	for idx, key := range keys {
		start := time.Now()
		httpStatus := 0
		result, err := s.callModerationOnceWithInput(ctx, cfg, key, testInput, &httpStatus)
		// Truncate elapsed duration to the integer milliseconds used by key health.
		latency := int(time.Since(start).Milliseconds())
		keyHash := moderationAPIKeyHash(key)
		if err != nil {
			s.markAPIKeyError(key, err.Error(), latency, httpStatus)
		} else {
			s.markAPIKeySuccess(key, latency, httpStatus)
			if auditResult == nil && len(result) > 0 {
				auditResult = buildContentModerationTestAuditResult(&result[0], cfg.Thresholds)
			}
		}
		priority := defaultContentModerationAPIKeyPriority
		note := ""
		if configured {
			metadata := cfg.apiKeyMetadataForHash(keyHash)
			priority = metadata.Priority
			note = metadata.Note
		}
		status := s.apiKeyStatusForHash(idx, keyHash, maskSecretTail(key), configured, priority, note)
		status.LastTested = true
		items = append(items, status)
	}
	return &TestContentModerationAPIKeysResult{Items: items, AuditResult: auditResult, ImageCount: imageCount}, nil
}

// ErrorText joins failed-unit kind, index, and public error text for storage
// diagnostics. Nil results and empty failure lists return empty text, and the
// join follows the failure order recorded by the merger.
func (r *contentModerationAuditResult) ErrorText() string {
	if r == nil || len(r.FailedUnits) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.FailedUnits))
	for _, unit := range r.FailedUnits {
		parts = append(parts, fmt.Sprintf("%s[%d]: %s", unit.Type, unit.Index, unit.Error))
	}
	return strings.Join(parts, "; ")
}

// Error returns the moderation HTTP status and captured message as diagnostic
// text. A nil error receiver returns a generic label; public audit records
// separately replace this diagnostic text with status-class messages.
func (e *contentModerationAPIError) Error() string {
	if e == nil {
		return "moderation api error"
	}
	return fmt.Sprintf("moderation api status %d: %s", e.StatusCode, e.Message)
}

// auditContentModerationInput collects maximum category scores and
// incomplete-unit observations across text batches and images. Text batches
// execute sequentially first. Up to four image workers then submit
// independently completing unit results to one consumer, which owns all
// aggregate mutations.
//
// Cancellation makes accepted image jobs report failure but does not abandon
// their result sends; the consumer drains until the producer closes results
// after joining workers. That ordering retains each unit outcome and prevents a
// blocked result sender from being left behind. AuditComplete reports whether
// any failed unit was recorded, while successful scores can still support a
// blocking decision.
func (s *ContentModerationService) auditContentModerationInput(ctx context.Context, cfg *ContentModerationConfig, content ContentModerationInput, trackKeyLoad bool) *contentModerationAuditResult {
	result := &contentModerationAuditResult{
		CategoryScores: make(map[string]float64),
		TextUnitCount:  countContentModerationTextChunks(content.Text, maxModerationInputRunes, contentModerationChunkOverlap),
		ImageUnitCount: len(content.ImageItems),
	}
	forEachContentModerationTextBatch(content.Text, maxModerationInputRunes, contentModerationChunkOverlap, contentModerationTextBatchSize, func(batchStart int, chunks []string) {
		unitResults := s.auditTextBatch(ctx, cfg, chunks, batchStart, trackKeyLoad)
		for _, unit := range unitResults {
			mergeContentModerationUnitResult(result, unit, cfg.Thresholds)
		}
	})

	// Image workers publish individual outcomes to one merger after text completes;
	// draining every result keeps cancellation failures associated with
	// their accepted jobs.
	if len(content.ImageItems) > 0 {
		workerCount := min(contentModerationImageConcurrency, len(content.ImageItems))
		results := make(chan contentModerationUnitResult, workerCount)
		jobs := make(chan int)
		var wg sync.WaitGroup
		for range workerCount {
			wg.Go(func() {
				for index := range jobs {
					image := content.ImageItems[index]
					if err := ctx.Err(); err != nil {
						results <- contentModerationUnitResult{unitType: ContentModerationItemTypeImage, index: index, sourceIndex: image.SourceIndex, err: err}
						continue
					}
					parts := []moderationAPIInputPart{{Type: "image_url", ImageURL: &moderationAPIImageURLRef{URL: image.Reference}}}
					apiResult, err := s.callModeration(ctx, cfg, parts, trackKeyLoad)
					results <- contentModerationUnitResult{unitType: ContentModerationItemTypeImage, index: index, sourceIndex: image.SourceIndex, result: apiResult, err: err}
				}
			})
		}
		// Dispatch every image position, then close jobs and join workers before
		// closing results; the consumer stays available for all accepted result sends.
		go func() {
			for index := range content.ImageItems {
				jobs <- index
			}
			close(jobs)
			wg.Wait()
			close(results)
		}()
		for unit := range results {
			mergeContentModerationUnitResult(result, unit, cfg.Thresholds)
		}
	}
	result.AuditComplete = len(result.FailedUnits) == 0
	return result
}

// auditTextBatch associates one outcome with every supplied text chunk using
// startIndex. Empty chunks return nil, and a single chunk uses scalar API
// input. A successful batch is accepted only when result count matches input
// count, preserving positional association.
//
// Unsupported HTTP-400 batch messages and successful replies with mismatched
// counts retry chunks individually. Other failures are attached to every chunk
// without fan-out, limiting requests during provider failures. Each individual
// fallback still uses normal key selection and retry policy, and returned
// indexes remain the original batch positions.
func (s *ContentModerationService) auditTextBatch(ctx context.Context, cfg *ContentModerationConfig, chunks []string, startIndex int, trackKeyLoad bool) []contentModerationUnitResult {
	if len(chunks) == 0 {
		return nil
	}
	input := any(chunks)
	if len(chunks) == 1 {
		input = chunks[0]
	}
	results, err := s.callModerationBatch(ctx, cfg, input, trackKeyLoad)
	if err == nil && len(results) == len(chunks) {
		out := make([]contentModerationUnitResult, 0, len(results))
		for index := range results {
			item := results[index]
			out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, result: &item})
		}
		return out
	}
	resultCountMismatch := err == nil && len(results) != len(chunks)
	if resultCountMismatch {
		err = fmt.Errorf("moderation api returned %d results for %d inputs", len(results), len(chunks))
	}
	if len(chunks) == 1 {
		return []contentModerationUnitResult{{unitType: ContentModerationItemTypeText, index: startIndex, err: err}}
	}
	if !resultCountMismatch && !isContentModerationBatchUnsupportedError(err) {
		out := make([]contentModerationUnitResult, 0, len(chunks))
		for index := range chunks {
			out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, err: err})
		}
		return out
	}
	// Retry individual chunks when batch shape or result count prevents positional
	// association; other errors retain their batch scope so an upstream failure
	// does not fan out requests.
	out := make([]contentModerationUnitResult, 0, len(chunks))
	for index, chunk := range chunks {
		item, singleErr := s.callModeration(ctx, cfg, chunk, trackKeyLoad)
		out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, result: item, err: singleErr})
	}
	return out
}

// isContentModerationBatchUnsupportedError recognizes wrapped HTTP-400 errors
// containing selected lowercase batch-shape markers. Other statuses, error
// types, or messages return false; this classification permits individual
// retries in auditTextBatch. A matching typed error pointer must be nonnil
// because its status is read after errors.As succeeds.
func isContentModerationBatchUnsupportedError(err error) bool {
	var apiErr *contentModerationAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		return false
	}
	message := strings.ToLower(apiErr.Message)
	for _, marker := range []string{"batch", "array", "must be a string", "expected string", "unsupported input"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// mergeContentModerationUnitResult updates an aggregate using one completed
// unit and local category thresholds. Nil targets are ignored. Failed or
// missing results append a public error record; successful units increase the
// success count and retain each category maximum across all successful units.
//
// Image hits also append their audit image index for later review mapping.
// Threshold evaluation uses local scores rather than the provider Flagged bit,
// so independently successful units remain effective even when another unit
// fails. The aggregate is owned by the single result consumer and requires no
// concurrent mutation.
func mergeContentModerationUnitResult(target *contentModerationAuditResult, unit contentModerationUnitResult, thresholds map[string]float64) {
	if target == nil {
		return
	}
	// Store public failure classes instead of bounded provider diagnostics here,
	// keeping echoed request or credential text out of the retained
	// failed-unit message.
	if unit.err != nil || unit.result == nil {
		errText := "moderation api returned no result"
		if unit.err != nil {
			errText = publicContentModerationError(unit.err)
		}
		target.FailedUnits = append(target.FailedUnits, ContentModerationFailedUnit{Type: unit.unitType, Index: unit.index, SourceIndex: unit.sourceIndex, Error: errText})
		return
	}
	target.SuccessfulUnits++
	for category, score := range unit.result.CategoryScores {
		if current, ok := target.CategoryScores[category]; !ok || score > current {
			target.CategoryScores[category] = score
		}
	}
	flagged, _, _ := evaluateModerationScores(unit.result.CategoryScores, thresholds)
	if flagged && unit.unitType == ContentModerationItemTypeImage {
		target.FlaggedImageIndexes = append(target.FlaggedImageIndexes, unit.index)
	}
}

// publicContentModerationError maps usable wrapped provider errors to generic
// stored failure classes. A matching contentModerationAPIError pointer must be
// nonnil because its status is read after type matching; other errors,
// including nil, receive a generic request-failed message.
func publicContentModerationError(err error) string {
	if apiErr, ok := errors.AsType[*contentModerationAPIError](err); ok {
		switch {
		case apiErr.StatusCode == 429:
			return "moderation provider rate limited"
		case apiErr.StatusCode >= 500:
			return "moderation provider unavailable"
		case apiErr.StatusCode >= 400:
			return "moderation provider rejected request"
		}
	}
	return "moderation provider request failed"
}

// normalizeContentModerationChunkOptions returns a positive chunk size and
// overlap smaller than it. Nonpositive sizes use the audit default, negative
// overlap becomes zero, and excessive overlap clamps below size so every chunk
// advances.
//
// Both the chunk counter and iterator use these values, so they agree on the
// same positive step. An overlap at least as large as the normalized size
// becomes size minus one; changing only one consumer would make reported unit
// counts diverge from emitted chunks.
func normalizeContentModerationChunkOptions(chunkSize int, overlap int) (int, int) {
	if chunkSize <= 0 {
		chunkSize = maxModerationInputRunes
	}
	if overlap < 0 {
		overlap = 0
	}
	// Keep a positive read step for overlapping chunks.
	if overlap >= chunkSize {
		overlap = chunkSize - 1
	}
	return chunkSize, overlap
}

// countContentModerationTextChunks returns the number of overlapping rune
// chunks for nonblank aggregate text. Whitespace-only input yields zero. Size
// and overlap use the same normalization as the iterator, guaranteeing a
// positive step.
//
// The first chunk covers up to chunkSize runes; only text beyond it contributes
// additional steps. Rounding those remaining steps up counts a partial tail
// while an exact full final chunk creates no overlap-only extra unit. Batch
// construction and audit status use this count, so its arithmetic follows the
// iterator termination rule.
func countContentModerationTextChunks(text string, chunkSize int, overlap int) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	chunkSize, overlap = normalizeContentModerationChunkOptions(chunkSize, overlap)
	runeCount := utf8.RuneCountInString(text)
	if runeCount <= chunkSize {
		return 1
	}
	step := chunkSize - overlap
	// Count the partial final step produced by the chunk iterator.
	return 1 + (runeCount-chunkSize+step-1)/step
}

// forEachContentModerationTextBatch visits ordered rune chunks with original
// chunk start indexes in bounded batches. Blank text or a nil visitor
// contributes nothing; nonpositive sizes and batch limits select defaults, and
// overlap clamps below the normalized chunk size.
//
// The iterator carries the previous tail into the next chunk while reading new
// runes from the string. An exact final full chunk ends iteration instead of
// generating an overlap-only tail. Each submitted batch owns its slice; the
// final partial batch is visited once, and text is reconstructed from read
// runes, including replacement runes for invalid UTF-8 bytes.
func forEachContentModerationTextBatch(text string, chunkSize int, overlap int, batchSize int, visit func(startIndex int, chunks []string)) {
	if strings.TrimSpace(text) == "" || visit == nil {
		return
	}
	chunkSize, overlap = normalizeContentModerationChunkOptions(chunkSize, overlap)
	if batchSize <= 0 {
		batchSize = contentModerationTextBatchSize
	}
	reader := strings.NewReader(text)
	carry := make([]rune, 0, overlap)
	batch := make([]string, 0, batchSize)
	batchStart := 0
	chunkIndex := 0
	for {
		chunkRunes := make([]rune, len(carry), chunkSize)
		copy(chunkRunes, carry)
		reachedEOF := false
		for len(chunkRunes) < chunkSize {
			r, _, err := reader.ReadRune()
			if errors.Is(err, io.EOF) {
				reachedEOF = true
				break
			}
			// strings.Reader reports EOF for exhaustion; if another read error reaches
			// this branch, retain the collected runes and end further chunking.
			if err != nil {
				reachedEOF = true
				break
			}
			chunkRunes = append(chunkRunes, r)
		}
		// A complete final chunk already contains the stream tail. Stop here so
		// carrying overlap would not create another unit containing repeated text alone.
		if len(chunkRunes) == chunkSize && reader.Len() == 0 {
			reachedEOF = true
		}
		if len(chunkRunes) == 0 {
			break
		}
		batch = append(batch, string(chunkRunes))
		chunkIndex++
		if len(batch) == batchSize {
			visit(batchStart, batch)
			batchStart = chunkIndex
			batch = make([]string, 0, batchSize)
		}
		if reachedEOF {
			break
		}
		// Retain the previous chunk tail before reading new runes, preserving the
		// configured shared overlap while the iterator avoids retaining
		// a full-input rune slice.
		carry = append(carry[:0], chunkRunes[len(chunkRunes)-overlap:]...)
	}
	if len(batch) > 0 {
		visit(batchStart, batch)
	}
}

// contentModerationHitMedia returns pending snapshots for distinct valid
// indexes into selected audit ImageItems. Invalid and repeated indexes are
// skipped, while output follows the first supplied index order. Each entry
// keeps the selected image reference, source label, and original review-item
// SourceIndex.
//
// checkSync passes the filtered audit input with the result indexes, because
// source exclusions can make selected image positions differ from full review
// positions. Indexes select the audit collection; SourceIndex carries the
// review identity into snapshot metadata. Using the full unfiltered collection
// here could attach a hit to another submitted image.
func contentModerationHitMedia(content ContentModerationInput, indexes []int) []ContentModerationMedia {
	media := make([]ContentModerationMedia, 0, len(indexes))
	seen := make(map[int]struct{}, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(content.ImageItems) {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		image := content.ImageItems[index]
		media = append(media, ContentModerationMedia{SourceIndex: image.SourceIndex, Source: image.Source, OriginalRef: image.Reference, SnapshotStatus: "pending"})
	}
	return media
}

// callModeration returns the first result from a normal key-selected audit
// attempt. It forwards the optional first load-tracking flag to
// callModerationBatch and returns errors or an empty-result failure before
// exposing a result pointer.
func (s *ContentModerationService) callModeration(ctx context.Context, cfg *ContentModerationConfig, input any, trackKeyLoad ...bool) (*moderationAPIResult, error) {
	results, err := s.callModerationBatch(ctx, cfg, input, trackKeyLoad...)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("moderation api returned empty results")
	}
	return &results[0], nil
}

// callModerationBatch chooses an unfrozen weighted key for each attempt and
// returns the first successful result set. RetryCount adds attempts after the
// initial call, clamped to the supported limit. HTTP 400 ends attempts
// immediately; other failures update health and wait a linearly increasing
// delay before selecting again.
//
// Optional load tracking records each synchronous attempt independently from
// key health. Cancellation during the retry wait returns ctx.Err, while
// exhausted attempts return the last recorded failure. Keys can freeze between
// attempts, so unavailable selection can replace an earlier HTTP failure with
// the no-key result. This method does not validate positional result count;
// auditTextBatch owns that association.
func (s *ContentModerationService) callModerationBatch(ctx context.Context, cfg *ContentModerationConfig, input any, trackKeyLoad ...bool) ([]moderationAPIResult, error) {
	attempts := cfg.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}
	// Clamp total attempts, including the initial provider exchange.
	if attempts > maxContentModerationRetryCount+1 {
		attempts = maxContentModerationRetryCount + 1
	}
	trackLoad := len(trackKeyLoad) > 0 && trackKeyLoad[0]
	var lastErr error
	for attempt := range attempts {
		key, ok := s.nextUsableAPIKey(cfg)
		if !ok {
			lastErr = errors.New("no moderation api key available")
			break
		}
		if trackLoad {
			s.beginModerationAPIKeyCall(key)
		}
		start := time.Now()
		httpStatus := 0
		result, err := s.callModerationOnceWithInput(ctx, cfg, key, input, &httpStatus)
		// Truncate elapsed duration to the integer milliseconds used by key health.
		latency := int(time.Since(start).Milliseconds())
		if err == nil {
			if trackLoad {
				s.finishModerationAPIKeyCall(key, latency, true)
			}
			s.markAPIKeySuccess(key, latency, httpStatus)
			return result, nil
		}
		if trackLoad {
			s.finishModerationAPIKeyCall(key, latency, false)
		}
		s.markAPIKeyError(key, err.Error(), latency, httpStatus)
		lastErr = err
		// A request-shape rejection is retained for batch compatibility handling;
		// retrying other credentials here would resend the same invalid
		// shape immediately.
		if httpStatus == 400 {
			break
		}
		if attempt == attempts-1 {
			break
		}
		// Delay subsequent attempts while still selecting their key afresh; the
		// shared caller context can end this wait before another
		// provider exchange begins.
		wait := time.Duration(100*(attempt+1)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

// callModerationOnceWithInput serializes one audit request and returns a
// nonempty provider result set or an error. It joins the configured base with
// /v1/moderations and applies TimeoutMS to the HTTP request context. Proxy
// lookup captures the supplied parent context, so its storage resolution is not
// shortened by that child HTTP timeout.
//
// Received status is written to httpStatus when supplied, including exchange or
// decode errors. Non-2xx bodies become diagnostic contentModerationAPIErrors;
// successful replies with no result are rejected. The caller owns retry, health
// updates, and matching returned results to its input units.
func (s *ContentModerationService) callModerationOnceWithInput(ctx context.Context, cfg *ContentModerationConfig, key string, input any, httpStatus *int) ([]moderationAPIResult, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	endpoint, err := url.JoinPath(base, "/v1/moderations")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(moderationAPIRequest{Model: cfg.Model, Input: input})
	if err != nil {
		return nil, err
	}
	// Interpret TimeoutMS as this HTTP child budget; proxy resolution retains
	// the parent context so that storage lookup can outlast this child deadline.
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	var out moderationAPIResponse
	status, body, err := s.runtime.Audit.Execute(reqCtx, endpoint, key, raw, func() (string, error) {
		if cfg.ProxyID == nil {
			return "", nil
		}
		return s.resolveModerationProxyURL(ctx, *cfg.ProxyID)
	}, &out)
	if httpStatus != nil {
		*httpStatus = status
	}
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &contentModerationAPIError{StatusCode: status, Message: strings.TrimSpace(string(body))}
	}
	if len(out.Results) == 0 {
		return nil, errors.New("moderation api returned empty results")
	}
	return out.Results, nil
}

// resolveModerationProxyURL returns the selected stored proxy URL, reusing its
// matching unexpired cache entry. Missing lookup capability or lookup errors
// stop the audit attempt. Inactive or expired records generate diagnostics and
// still provide their URL; this method does not replace them with a direct
// connection.
//
// Successful resolutions publish one ID/URL entry expiring one minute after
// lookup starts. Lookups are not coalesced, so concurrent misses can each read
// and publish. Cache clearing during configuration or repository changes cannot
// prevent an already running lookup from publishing afterward, and callers
// bind repository ownership before concurrent requests begin.
func (s *ContentModerationService) resolveModerationProxyURL(ctx context.Context, proxyID int64) (string, error) {
	now := time.Now()
	previous := s.moderationProxyCache.Load()
	if previous != nil && previous.proxyID == proxyID && now.Before(previous.expiresAt) {
		return previous.url, nil
	}
	if s.proxyRepo == nil {
		return "", errors.New("moderation proxy repository unavailable")
	}
	proxy, err := s.proxyRepo.Lookup(ctx, proxyID, now)
	if err != nil {
		return "", fmt.Errorf("resolve moderation proxy %d: %w", proxyID, err)
	}
	if !proxy.Active || proxy.Expired {
		slog.Warn("content_moderation.proxy_not_active",
			"proxy_id", proxyID,
			"proxy_name", proxy.Name,
			"status", proxy.Status,
			"expired", proxy.Expired)
	}
	proxyURL := proxy.URL
	if previous == nil || previous.proxyID != proxyID || previous.url != proxyURL {
		// Log the adapter-provided address label because the dial URL can include
		// authentication; the display label is constructed separately
		// by app proxy lookup.
		slog.Info("content_moderation.proxy_enabled",
			"proxy_id", proxyID,
			"proxy_name", proxy.Name,
			"proxy_addr", proxy.Address)
	}
	// Publish ID, URL, and expiry together so a hit matches one resolved
	// selection; lookup latency consumes the minute measured from its start.
	s.moderationProxyCache.Store(&moderationProxyURLCacheEntry{
		proxyID:   proxyID,
		url:       proxyURL,
		expiresAt: now.Add(contentModerationProxyURLCacheTTL),
	})
	return proxyURL, nil
}

// buildModerationTestInput creates scalar text or text/image API parts for
// administrator probes. It trims and rune-caps the prompt, accepts at most
// eight nonblank validated inline references, and returns hello when neither
// text nor images contributes content.
func buildModerationTestInput(prompt string, images []string) (any, int, error) {
	// Cap probe text to the size of one production audit chunk.
	prompt = trimRunes(normalizeContentModerationText(prompt), maxModerationInputRunes)
	normalizedImages := make([]string, 0, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		// Reject later probe images beyond the supported per-request count.
		if len(normalizedImages) >= maxContentModerationTestImages {
			return nil, 0, infraerrors.BadRequest("TOO_MANY_MODERATION_TEST_IMAGES", fmt.Sprintf("At most %d test images can be uploaded", maxContentModerationTestImages))
		}
		if err := validateModerationTestImageDataURL(image); err != nil {
			return nil, 0, err
		}
		normalizedImages = append(normalizedImages, image)
	}
	// With no audit content, use a harmless fixed probe to observe credential
	// health; TestAPIKeys treats this intent differently from a supplied
	// content audit.
	if prompt == "" && len(normalizedImages) == 0 {
		return "hello", 0, nil
	}
	if len(normalizedImages) == 0 {
		return prompt, 0, nil
	}
	parts := make([]moderationAPIInputPart, 0, len(normalizedImages)+1)
	if prompt != "" {
		parts = append(parts, moderationAPIInputPart{Type: "text", Text: prompt})
	}
	for _, image := range normalizedImages {
		parts = append(parts, moderationAPIInputPart{
			Type:     "image_url",
			ImageURL: &moderationAPIImageURLRef{URL: image},
		})
	}
	return parts, len(normalizedImages), nil
}

// contentModerationTestHasAuditInput reports whether trimmed prompt or any
// nonblank image string represents administrator audit intent. It does not
// validate image format; buildModerationTestInput performs that validation
// before the selected key is called.
func contentModerationTestHasAuditInput(prompt string, images []string) bool {
	if normalizeContentModerationText(prompt) != "" {
		return true
	}
	for _, image := range images {
		if strings.TrimSpace(image) != "" {
			return true
		}
	}
	return false
}

// validateModerationTestImageDataURL checks encoded length, literal image data
// prefix, a base64 header marker, standard base64 decoding, and decoded byte
// size. It does not sniff image bytes or validate a full MIME subtype; those
// snapshot checks are separate.
func validateModerationTestImageDataURL(value string) error {
	// Reject oversized encoded input before allocating decoded probe bytes.
	if len(value) > maxContentModerationTestImageDataURLBytes {
		return infraerrors.BadRequest("MODERATION_TEST_IMAGE_TOO_LARGE", "Test images must not exceed 8MB")
	}
	if !strings.HasPrefix(value, "data:image/") {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "Test images must use data:image/* base64 encoding")
	}
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "Test images must be base64 data URLs")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "Invalid test image base64 encoding")
	}
	// Decoded bytes have their own cap despite encoded-length acceptance.
	if len(raw) > maxContentModerationTestImageBytes {
		return infraerrors.BadRequest("MODERATION_TEST_IMAGE_TOO_LARGE", "Test images must not exceed 8MB")
	}
	return nil
}

// buildContentModerationTestAuditResult copies scores and effective
// known-category thresholds for administrator display. Nil results return nil.
// It ignores the provider Flagged bit, applies local threshold evaluation, and
// reports the highest score again as CompositeScore.
//
// Unknown category scores remain in display data and can win its maximum while
// only known thresholds decide Flagged. Returned score and threshold maps can
// be mutated without changing the supplied maps; comparisons still use ordinary
// floating-point semantics.
func buildContentModerationTestAuditResult(result *moderationAPIResult, thresholds map[string]float64) *ContentModerationTestAuditResult {
	if result == nil {
		return nil
	}
	scores := make(map[string]float64, len(result.CategoryScores))
	maps.Copy(scores, result.CategoryScores)
	thresholdSnapshot := mergeContentModerationThresholds(ContentModerationDefaultThresholds(), thresholds)
	flagged, highestCategory, highestScore := evaluateModerationScores(scores, thresholdSnapshot)
	// The administrator composite display repeats the maximum category score.
	compositeScore := highestScore
	return &ContentModerationTestAuditResult{
		Flagged:         flagged,
		HighestCategory: highestCategory,
		HighestScore:    highestScore,
		CompositeScore:  compositeScore,
		CategoryScores:  scores,
		Thresholds:      thresholdSnapshot,
	}
}

// evaluateModerationScores returns local flagging and the displayed highest
// category and score. Ordered known categories compare their score with
// threshold using >=; missing map values read as zero, so a zero or absent
// threshold can flag a missing category. Provider Flagged does not participate.
//
// Known-category ties keep the first ordered category. Unknown scores can
// become the displayed maximum but never trigger flagging here; equal unknown
// maxima follow map iteration order when they exceed the known maximum. Callers
// normally supply the full normalized threshold map to make absence behavior
// deliberate.
func evaluateModerationScores(scores map[string]float64, thresholds map[string]float64) (bool, string, float64) {
	flagged := false
	highestCategory := ""
	highestScore := 0.0
	// Known missing scores read as zero during threshold comparison, and fixed
	// order keeps the first known maximum when scores tie across those categories.
	for _, category := range contentModerationCategoryOrder {
		score := scores[category]
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
		if score >= thresholds[category] {
			flagged = true
		}
	}
	// Include unknown scores in display maxima after flagging decisions; a larger
	// unknown value can change the displayed category while leaving
	// flagging unchanged.
	for category, score := range scores {
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
	}
	return flagged, highestCategory, highestScore
}
