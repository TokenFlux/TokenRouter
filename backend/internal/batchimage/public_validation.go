package batchimage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// public_validation.go normalizes batch-image submission requests, expands
// repeated item counts into deterministic custom IDs, enforces byte and
// attachment caps, and hashes request payloads for idempotency checks.
// Validation trims titles, prompts, and reference labels, verifies inline or
// gs:// reference images against MIME and per-attachment limits, and charges
// repeated outputs against whole-job output, reference, and inline-byte caps
// before checking expanded custom-ID uniqueness.
//
// Metadata sanitization sorts keys before enforcing count and length bounds so
// ordinary and composite-key request hashes remain deterministic across
// retries while isolating group-specific composite replays.

// ValidateSubmitRequest normalizes a batch and expands OutputCount
// repetitions into independent custom IDs. It supplies a title, MIME type,
// size and missing IDs, requires 1K output, and rejects empty prompts or
// workloads above configured caps. Prompt and title limits measure bytes.
//
// The request value retains shared item storage, so trimming original items
// can modify the caller's slice before the returned request receives a new
// expanded slice. Reference byte buffers remain shared across repeats.
// Reference counts and inline byte totals are multiplied by each output
// count, and collisions are checked after ID suffix expansion. The resulting
// title, metadata and expanded items feed the submission fingerprint.
func (s *Public) ValidateSubmitRequest(req BatchImageSubmitRequest) (BatchImageSubmitRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	req.TaskName = strings.TrimSpace(req.TaskName)
	req.ParentBatchID = strings.TrimSpace(req.ParentBatchID)
	req.Platform = strings.TrimSpace(req.Platform)
	req.ResponseMimeType = strings.TrimSpace(req.ResponseMimeType)
	req.AspectRatio = strings.TrimSpace(req.AspectRatio)
	req.ImageSize = strings.TrimSpace(req.ImageSize)
	if req.Model == "" {
		return req, ErrBatchImageInvalidModel
	}
	if req.TaskName == "" {
		req.TaskName = DefaultBatchImageTaskName(s.now())
	}
	// Byte-cap long task titles before they enter persistent records and the
	// normalized request fingerprint.
	if len(req.TaskName) > 255 {
		req.TaskName = TruncateBatchImageMessage(req.TaskName, 255)
	}
	if req.Platform != "" && !IsSupportedBatchImageProvider(req.Platform) {
		return req, ErrBatchImageUnsupportedProvider
	}
	if len(req.Items) == 0 {
		return req, ErrBatchImageInvalidItems
	}
	MaxItems := s.MaxItems()
	if len(req.Items) > MaxItems {
		return req, ErrBatchImageInvalidItems
	}
	if req.ResponseMimeType == "" {
		req.ResponseMimeType = s.DefaultResponseMimeType()
	}
	if req.ImageSize == "" {
		req.ImageSize = s.DefaultImageSize()
	}
	// Case-insensitive 1K preferences share one normalized spelling, keeping
	// size-dependent pricing and request fingerprints consistent across valid retries.
	if !strings.EqualFold(req.ImageSize, DefaultBatchImageImageSize) {
		return req, ErrBatchImageInvalidItems
	}
	req.ImageSize = DefaultBatchImageImageSize
	req.Metadata = SanitizeBatchImageMetadata(req.Metadata)

	seen := make(map[string]struct{}, len(req.Items))
	totalReferenceImages := 0
	totalInlineReferenceBytes := 0
	totalOutputImages := 0
	expandedItems := make([]BatchImageSubmitItem, 0, len(req.Items))
	for i := range req.Items {
		req.Items[i].CustomID = strings.TrimSpace(req.Items[i].CustomID)
		if req.Items[i].CustomID == "" {
			req.Items[i].CustomID = fmt.Sprintf("item_%06d", i+1)
		}
		outputCount := req.Items[i].OutputCount
		if outputCount == 0 {
			outputCount = 1
		}
		if outputCount < 1 || outputCount > s.MaxOutputImagesPerItem() {
			return req, ErrBatchImageInvalidItems
		}
		totalOutputImages += outputCount
		if totalOutputImages > s.MaxOutputImagesPerJob() {
			return req, ErrBatchImageTooManyOutputImages
		}
		req.Items[i].Prompt = strings.TrimSpace(req.Items[i].Prompt)
		if req.Items[i].Prompt == "" {
			return req, ErrBatchImageInvalidItems
		}
		// The prompt budget measures storage bytes, so multibyte text consumes more
		// of the configured limit than the same number of ASCII characters.
		if len(req.Items[i].Prompt) > s.MaxPromptChars() {
			return req, ErrBatchImagePromptTooLong
		}
		referenceCount, inlineReferenceBytes, err := NormalizeBatchImageReferenceInputs(&req.Items[i])
		if err != nil {
			return req, err
		}
		// Each repeated output sends the same references again; charge expanded
		// attachment and byte totals against the whole-job caps before generating
		// those request IDs.
		totalReferenceImages += referenceCount * outputCount
		if totalReferenceImages > s.MaxReferenceImagesPerJob() {
			return req, ErrBatchImageTooManyReferenceImages
		}
		totalInlineReferenceBytes += inlineReferenceBytes * outputCount
		if totalInlineReferenceBytes > s.MaxReferenceInlineBytesPerJob() {
			return req, ErrBatchImageReferenceImagesTooLarge
		}
		// Expand before checking uniqueness: a repeated "cover" can collide with a
		// caller-supplied "cover_01", making provider results ambiguous after submission.
		for repeatIndex := 1; repeatIndex <= outputCount; repeatIndex++ {
			expanded := req.Items[i]
			expanded.OutputCount = 0
			if outputCount > 1 {
				expanded.CustomID = fmt.Sprintf("%s_%0*d", req.Items[i].CustomID, BatchImageRepeatSuffixWidth(outputCount), repeatIndex)
			}
			if _, ok := seen[expanded.CustomID]; ok {
				return req, ErrBatchImageDuplicateCustomIDInRequest
			}
			seen[expanded.CustomID] = struct{}{}
			expandedItems = append(expandedItems, expanded)
		}
	}
	req.Items = expandedItems
	return req, nil
}

// NormalizeBatchImageReferenceInputs validates and replaces an item's
// reference slice with normalized values, retaining shared byte buffers. Each
// reference needs an accepted image MIME and exactly one inline or gs://
// source; the result counts attachments and inline bytes.
func NormalizeBatchImageReferenceInputs(item *BatchImageSubmitItem) (int, int, error) {
	if item == nil || len(item.ReferenceImages) == 0 {
		return 0, 0, nil
	}
	out := make([]BatchImageReferenceInput, 0, len(item.ReferenceImages))
	inlineBytes := 0
	for _, ref := range item.ReferenceImages {
		// Bound reference labels before provider input and hashing so optional
		// identifying text cannot enlarge the normalized request indefinitely.
		ref.ID = TruncateBatchImageMessage(strings.TrimSpace(ref.ID), 80)
		ref.Type = TruncateBatchImageMessage(strings.TrimSpace(ref.Type), 40)
		ref.MimeType = NormalizeBatchImageReferenceMimeType(ref.MimeType)
		ref.FileURI = strings.TrimSpace(ref.FileURI)
		if ref.MimeType == "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) == 0 && ref.FileURI == "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) > 0 && ref.FileURI != "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		// Reject oversized decoded attachments before repeated-work byte totals
		// are calculated for the entire batch.
		if len(ref.Data) > MaxBatchImageReferenceImageBytes {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if ref.FileURI != "" && !strings.HasPrefix(ref.FileURI, "gs://") {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		inlineBytes += len(ref.Data)
		out = append(out, ref)
	}
	item.ReferenceImages = out
	return len(out), inlineBytes, nil
}

// BatchImageRepeatSuffixWidth returns the decimal width used for expanded
// output IDs. Counts below ten use two digits, making repeated custom IDs
// readable and consistent with the public submission examples.
func BatchImageRepeatSuffixWidth(count int) int {
	if count < 10 {
		return 2
	}
	return len(strconv.Itoa(count))
}

// MaxItems returns the positive configured cap on submitted prompt entries. A
// nil service or nonpositive setting uses two hundred; repetition expansion
// is governed separately by the output image cap.
func (s *Public) MaxItems() int {
	if s != nil && s.Options.MaxItemsPerJobDefault > 0 {
		return s.Options.MaxItemsPerJobDefault
	}
	return DefaultBatchImageMaxItems
}

// MaxOutputImagesPerJob returns the positive configured cap on expanded
// outputs across one submission. Missing or nonpositive settings use two
// hundred, and validation counts every OutputCount repetition against this
// total.
func (s *Public) MaxOutputImagesPerJob() int {
	if s != nil && s.Options.MaxOutputImagesPerJob > 0 {
		return s.Options.MaxOutputImagesPerJob
	}
	return DefaultBatchImageMaxOutputImages
}

// MaxOutputImagesPerItem returns the positive configured repetition cap for
// each submitted prompt. Missing or nonpositive settings use four, while a
// zero OutputCount is normalized to one before checking this cap.
func (s *Public) MaxOutputImagesPerItem() int {
	if s != nil && s.Options.MaxOutputImagesPerItem > 0 {
		return s.Options.MaxOutputImagesPerItem
	}
	return DefaultBatchImageMaxOutputCount
}

// MaxPromptChars returns the configured positive byte cap used for prompts
// and stored previews. Missing or nonpositive settings use eight thousand,
// and validation applies len(string) rather than counting Unicode characters.
func (s *Public) MaxPromptChars() int {
	if s != nil && s.Options.MaxPromptCharsPerItem > 0 {
		return s.Options.MaxPromptCharsPerItem
	}
	return DefaultBatchImageMaxPromptChars
}

// MaxReferenceImagesPerJob returns the configured positive attachment cap for
// the expanded batch workload. Missing or nonpositive settings use one
// thousand; references repeated by OutputCount contribute once per expanded
// output.
func (s *Public) MaxReferenceImagesPerJob() int {
	if s != nil && s.Options.MaxReferenceImagesPerJob > 0 {
		return s.Options.MaxReferenceImagesPerJob
	}
	return DefaultBatchImageMaxReferenceImages
}

// MaxReferenceInlineBytesPerJob returns the configured positive decoded
// inline-byte cap for the expanded workload. Missing or nonpositive settings
// use 128 MiB, and repeated attachments contribute their byte length for
// every output.
func (s *Public) MaxReferenceInlineBytesPerJob() int {
	if s != nil && s.Options.MaxReferenceInlineBytesPerJob > 0 {
		return s.Options.MaxReferenceInlineBytesPerJob
	}
	return DefaultBatchImageMaxReferenceBytes
}

// DefaultResponseMimeType returns a trimmed configured output MIME value when
// it is present. A nil service or blank setting uses image/png, which request
// normalization applies when the caller omitted its preference.
func (s *Public) DefaultResponseMimeType() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultResponseMimeType) != "" {
		return strings.TrimSpace(s.Options.DefaultResponseMimeType)
	}
	return DefaultBatchImageResponseMime
}

// DefaultImageSize returns a trimmed configured size when it is present.
// Missing or blank settings use 1K; ValidateSubmitRequest then checks that
// the selected default satisfies the supported batch size.
func (s *Public) DefaultImageSize() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Options.DefaultImageSize)
	}
	return DefaultBatchImageImageSize
}

// HashBatchImageSubmitRequest returns hexadecimal SHA-256 of the request JSON
// after metadata sanitation. Callers supply the normalized request; SessionID
// is JSON-excluded, while the normalized title, item IDs and input data
// affect the fingerprint.
func HashBatchImageSubmitRequest(req BatchImageSubmitRequest) string {
	req.Metadata = SanitizeBatchImageMetadata(req.Metadata)
	// These request fields are JSON-encodable strings, byte slices and containers.
	// Discard the encoding error before hashing the resulting request representation.
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashCompositeBatchImageSubmitRequest fingerprints the normalized request
// and selected group identity. Submit retains the original client spelling
// separately, so aliases selecting the same internal model and group can share
// this content fingerprint. Another group changes the encoded payload even
// when prompts and the internal model match.
//
// The group pointer is copied before JSON encoding, and metadata uses the same
// sanitation as ordinary submission hashing. SessionID remains excluded by its
// request tag. The hexadecimal digest lets an owner-scoped idempotency key
// reject a retry whose normalized content has moved into another group.
func HashCompositeBatchImageSubmitRequest(req BatchImageSubmitRequest, groupID *int64) string {
	payload := struct {
		Request BatchImageSubmitRequest `json:"request"`  // normalized submission request snapshot
		GroupID *int64                  `json:"group_id"` // selected routing group
	}{
		Request: req,
		GroupID: CloneInt64Ptr(groupID),
	}
	payload.Request.Metadata = SanitizeBatchImageMetadata(payload.Request.Metadata)
	// The wrapper contains the JSON-encodable request and one optional integer.
	// Encoding retains group identity alongside the normalized request fields.
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SanitizeBatchImageMetadata builds a fresh map of trimmed labels by visiting
// input keys in sorted order. Nil or empty input returns nil. Blank keys and
// keys above 64 bytes are skipped, values are capped at 256 bytes, and at most
// twenty normalized keys survive.
//
// Trimming can merge distinct input keys: " title" and "title" address the same
// output entry. The later sorted input replaces that value if the cap has not
// already stopped traversal. Sorting therefore fixes both collision precedence
// and the selected subset used by submission hashing.
func SanitizeBatchImageMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		key := strings.TrimSpace(k)
		if key == "" || len(key) > 64 {
			continue
		}
		value := strings.TrimSpace(in[k])
		if len(value) > 256 {
			// Metadata values retain a bounded byte prefix before hashing and provider
			// submission, even for long caller-supplied labels.
			value = value[:256]
		}
		out[key] = value
		// Sorted input keys determine which normalized labels survive the
		// twenty-entry request metadata cap.
		if len(out) >= 20 {
			break
		}
	}
	return out
}
