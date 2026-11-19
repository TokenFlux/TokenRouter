package creative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/webp"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// public_validation.go validates Creative Studio creation parameters, decodes
// source and mask image dimensions, builds idempotency fingerprints, and runs
// no-media-retention content moderation.
// Submission checks user binding, group image gates, concrete model
// allowlists, and per-operation capability choices before normalizing upload
// MIME labels against leading byte signatures and enforcing raw and base64
// request size budgets.
//
// Inpaint requests verify PNG mask dimensions against the first source image,
// normalized controls and ordered media SHA-256 digests form the workspace
// idempotency fingerprint, and moderation failures fail open on service outage
// while blocking confirmed policy denials before funding.

// ValidatedCreativeParams retains normalized creation controls, source bytes,
// and distinct public/upstream model identities.
type ValidatedCreativeParams struct {
	OutputFormat    string `json:"output_format,omitempty"`     // explicit animation format selection
	FrameCount      int    `json:"frame_count,omitempty"`       // normalized animation frame count
	FrameDurationMS int    `json:"frame_duration_ms,omitempty"` // milliseconds per animation frame

	// Model, FinalModel, and Platform retain public identity, resolved upstream
	// identity, and route selection separately. Sources and Mask retain caller
	// bytes with normalized MIME labels and hashes; callers keep them stable
	// until saved.
	Platform string // selected image route platform
	// Group retains the validated reader view. Pricing and creation read its
	// identifier and rate afterward, so callers keep it stable until provisioning
	// finishes.
	Group *GroupView // validated current group view
	Model string     // public request model name
	// FinalModel is the first eligible upstream name in the chosen platform
	// route used for creation capabilities and pricing. The worker resolves its
	// actually selected provider model during preparation.
	FinalModel    string               // resolved upstream image model
	Operation     string               // normalized creative operation name
	Prompt        string               // trimmed plaintext submitted prompt
	PromptHash    string               // SHA-256 prompt digest
	ImageSize     string               // normalized image size tier
	AspectRatio   string               // normalized image shape option
	Quality       string               // normalized image quality option
	Background    string               // normalized image background option
	ThinkingLevel string               // normalized image reasoning option
	OutputCount   int                  // validated requested image count
	Sources       []CreativeInputImage // normalized source image values
	// Mask is present for validated inpaint input and carries PNG bytes whose
	// dimensions match the first source. Other creation operations reject a
	// supplied mask.
	Mask        *CreativeInputImage // optional normalized mask image
	Fingerprint string              // SHA-256 normalized request digest
}

// CreativeFingerprintPayload fixes normalized request JSON field order and
// carries ordered media content digests.
type CreativeFingerprintPayload struct {
	// OutputFormat, FrameCount, and FrameDurationMS carry animation controls only.
	// Static requests retain omitted fields so their existing idempotency fingerprints
	// remain compatible with tasks accepted before animation support.

	OutputFormat    string `json:"output_format,omitempty"`     // explicit animation format selection
	FrameCount      int    `json:"frame_count,omitempty"`       // normalized animation frame count
	FrameDurationMS int    `json:"frame_duration_ms,omitempty"` // milliseconds per animation frame

	GroupID int64  `json:"group_id"` // target request group identifier
	Model   string `json:"model"`    // public request model name
	// normalized creative operation name
	Operation string `json:"operation"`
	// SHA-256 normalized prompt digest
	PromptSHA256 string `json:"prompt_sha256"`
	// ImageSHA256 retains source order in the JSON fingerprint payload. Nil
	// encodes null and an allocated empty slice encodes an empty array; validation
	// uses CreativeImageHashes to supply its canonical sequence shape.
	// ordered source SHA-256 digests
	ImageSHA256 []string `json:"image_sha256"`
	// optional mask SHA-256 digest
	MaskSHA256 string `json:"mask_sha256,omitempty"`
	ImageSize  string `json:"image_size"` // normalized image size tier
	// normalized image shape option
	AspectRatio string `json:"aspect_ratio"`
	// normalized image quality option
	Quality string `json:"quality,omitempty"`
	// normalized image background option
	Background string `json:"background,omitempty"`
	// normalized image reasoning option
	ThinkingLevel string `json:"thinking_level,omitempty"`
	OutputCount   int    `json:"output_count"` // validated requested image count
}

// ValidateCreateParams returns normalized controls, retained media, and
// request digests after current user/group/model admission. Its chosen
// operation capabilities control parameters before a task is created.
//
// Prompt limits count Unicode code points, raw media limits count bytes, and
// Gemini also checks a conservative encoded-request estimate. Edit requires a
// source; inpaint requires a PNG mask matching first-source dimensions; other
// operations reject a mask. Size, shape, quality, background, thinking, and
// count are resolved against capabilities. Ordered source digests and
// normalized controls form identity while public model, upstream model, and
// platform remain distinct route data.
func (s *Public) ValidateCreateParams(ctx context.Context, userID int64, params *CreateCreativeRunParamsPublic) (*ValidatedCreativeParams, error) {
	if params == nil {
		return nil, ErrCreativeInvalidParams
	}
	user, err := s.UserRepo.GetByID(ctx, userID)
	// Validation reports the configured user-not-found classification for any
	// failed access read or absent user, preventing group and model admission
	// from continuing without that required access value.
	if err != nil || user == nil {
		return nil, s.UserNotFound
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, params.GroupID)
	if err != nil || group == nil || !group.Active {
		return nil, ErrCreativeGroupForbidden
	}
	if !user.CanBindGroup(group.ID, group.IsExclusive) {
		return nil, ErrCreativeGroupForbidden
	}
	if group.ClaudeCodeOnly {
		return nil, ErrCreativeGroupForbidden
	}
	if !group.AllowImageGeneration {
		return nil, ErrCreativeGroupImageDisabled
	}
	operations := groupOperations(group)
	if len(operations) == 0 {
		return nil, ErrCreativeGroupImageDisabled
	}
	model := strings.TrimSpace(params.Model)
	modelSettings := CreativeModelSettingsIndex(s.CreativeModelSettings(ctx))
	configuredOperations, configured := CreativeOperationsForModel(modelSettings, group.ID, model, operations)
	if !configured {
		return nil, ErrCreativeInvalidModel
	}
	operations = configuredOperations
	if len(operations) == 0 {
		return nil, ErrCreativeOperationUnsupported
	}
	query, err := s.prepareCreativeModels(ctx, group)
	if err != nil {
		return nil, err
	}
	route, available, err := query.resolve(ctx, model, false)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, ErrCreativeInvalidModel
	}
	finalModel := route.Model
	if finalModel == "" {
		finalModel = model
	}
	operations = intersectCreativeOperations(operations, route.Operations)
	operation := strings.TrimSpace(params.Operation)
	operationAllowed := slices.Contains(operations, operation)
	if !operationAllowed {
		return nil, ErrCreativeOperationUnsupported
	}
	if attrs, found := route.OperationAttributes[operation]; found {
		route.Attributes = attrs
	}
	capabilities := CreativeCapabilitiesForModel(route.Platform, finalModel, route.Attributes)
	if capabilities.MaxReferenceImages > 0 && len(params.SourceImages) > capabilities.MaxReferenceImages {
		return nil, ErrCreativeInvalidParams
	}

	format := strings.ToLower(strings.TrimSpace(params.OutputFormat))
	if format == "png" {
		format = ""
	}
	if format != "" && format != "gif" {
		return nil, ErrCreativeInvalidParams
	}
	frames, duration, gifErr := protocol.NormalizeGIFOptions(format, params.FrameCount, params.FrameDurationMS)
	if gifErr != nil {
		return nil, ErrCreativeInvalidParams.WithCause(gifErr)
	}
	if format == "gif" && (operation == CreativeOperationInpaint || !slices.Contains(capabilities.OutputFormats, "gif")) {
		return nil, ErrCreativeOperationUnsupported
	}
	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		return nil, ErrCreativeInvalidParams
	}
	if utf8.RuneCountInString(prompt) > s.MaxPromptChars() {
		return nil, ErrCreativePromptTooLong
	}

	outputCount := params.OutputCount
	if outputCount == 0 {
		outputCount = 1
	}
	if outputCount < 1 || outputCount > capabilities.MaxOutputCount {
		return nil, ErrCreativeInvalidParams
	}

	imageSize := strings.TrimSpace(params.ImageSize)
	if imageSize == "" {
		imageSize = s.DefaultImageSize()
	}
	imageSize, supported := CreativeCanonicalOption(imageSize, creativeRouteImageSizes(route))
	if !supported {
		return nil, ErrCreativeInvalidParams
	}
	aspectRatio := strings.TrimSpace(params.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = CreativeDefaultOption(capabilities.AspectRatios, "auto")
	} else {
		aspectRatio, supported = CreativeCanonicalOption(aspectRatio, capabilities.AspectRatios)
		if !supported {
			return nil, ErrCreativeInvalidParams
		}
	}
	quality := strings.ToLower(strings.TrimSpace(params.Quality))
	if len(capabilities.Qualities) > 0 {
		if quality == "" {
			quality = CreativeDefaultOption(capabilities.Qualities, "medium")
		} else if !CreativeContainsOption(capabilities.Qualities, quality) {
			return nil, ErrCreativeInvalidParams
		}
	} else if quality != "" {
		return nil, ErrCreativeInvalidParams
	}
	background := strings.ToLower(strings.TrimSpace(params.Background))
	if len(capabilities.BackgroundOptions) > 0 {
		if background == "" {
			background = CreativeDefaultOption(capabilities.BackgroundOptions, "auto")
		} else if !CreativeContainsOption(capabilities.BackgroundOptions, background) {
			return nil, ErrCreativeInvalidParams
		}
	} else if background != "" {
		return nil, ErrCreativeInvalidParams
	}
	thinkingLevel := strings.ToLower(strings.TrimSpace(params.ThinkingLevel))
	if len(capabilities.ThinkingLevels) > 0 {
		if thinkingLevel == "" {
			thinkingLevel = CreativeDefaultOption(capabilities.ThinkingLevels, "minimal")
		} else if !CreativeContainsOption(capabilities.ThinkingLevels, thinkingLevel) {
			return nil, ErrCreativeInvalidParams
		}
	} else if thinkingLevel != "" {
		return nil, ErrCreativeInvalidParams
	}

	sources := make([]CreativeInputImage, 0, len(params.SourceImages))
	totalBytes := 0
	for i := range params.SourceImages {
		source, err := NormalizeCreativeImageInput(params.SourceImages[i], s.MaxAssetBytes())
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
		totalBytes += len(source.Bytes)
	}
	var mask *CreativeInputImage
	if params.Mask != nil && len(params.Mask.Bytes) > 0 {
		if len(params.Mask.Bytes) > MaxCreativeMaskBytes {
			return nil, ErrCreativeAssetTooLarge
		}
		normalized, err := NormalizeCreativeImageInput(*params.Mask, s.MaxAssetBytes())
		if err != nil {
			return nil, err
		}
		if normalized.Mime != "image/png" {
			return nil, ErrCreativeMaskRequired
		}
		mask = &normalized
		totalBytes += len(normalized.Bytes)
	}
	if totalBytes > 0 && int64(totalBytes) > s.MaxTotalInputBytes() {
		return nil, ErrCreativeInputTooLarge
	}
	if route.Platform == PlatformGemini {
		encodedBytes := base64.StdEncoding.EncodedLen(len([]byte(prompt)))
		for _, source := range sources {
			encodedBytes += base64.StdEncoding.EncodedLen(len(source.Bytes))
		}
		if mask != nil {
			encodedBytes += base64.StdEncoding.EncodedLen(len(mask.Bytes))
		}
		// Estimate base64 expansion, prompt bytes, and fixed JSON overhead before
		// Gemini inline execution. This conservative encoded total can exceed the
		// raw media limit because source bytes are serialized inside
		// request JSON.
		if int64(encodedBytes)+int64(len(prompt))+4096 > MaxCreativeGeminiInlineBytes {
			return nil, ErrCreativeInputTooLarge
		}
	}

	switch operation {
	case CreativeOperationEdit:
		if len(sources) == 0 {
			return nil, ErrCreativeInvalidParams
		}
	case CreativeOperationInpaint:
		if len(sources) == 0 || mask == nil {
			return nil, ErrCreativeMaskRequired
		}
		maskWidth, maskHeight, err := CreativeImageDimensions(mask.Bytes, mask.Mime)
		if err != nil {
			return nil, ErrCreativeMaskRequired
		}
		sourceWidth, sourceHeight, err := CreativeImageDimensions(sources[0].Bytes, sources[0].Mime)
		if err != nil {
			return nil, ErrCreativeInvalidMime
		}
		if maskWidth != sourceWidth || maskHeight != sourceHeight {
			return nil, ErrCreativeMaskSizeMismatch
		}
	}
	if mask != nil && operation != CreativeOperationInpaint {
		return nil, ErrCreativeInvalidParams
	}

	promptHash := Sha256Hex([]byte(prompt))
	fingerprint := BuildCreativeRequestFingerprint(CreativeFingerprintPayload{
		OutputFormat: format, FrameCount: frames, FrameDurationMS: duration,
		GroupID:       group.ID,
		Model:         model,
		Operation:     operation,
		PromptSHA256:  promptHash,
		ImageSHA256:   CreativeImageHashes(sources),
		MaskSHA256:    CreativeImageHash(mask),
		ImageSize:     imageSize,
		AspectRatio:   aspectRatio,
		Quality:       quality,
		Background:    background,
		ThinkingLevel: thinkingLevel,
		OutputCount:   outputCount,
	})
	return &ValidatedCreativeParams{
		OutputFormat: format, FrameCount: frames, FrameDurationMS: duration,
		Group:         group,
		Platform:      route.Platform,
		Model:         model,
		FinalModel:    finalModel,
		Operation:     operation,
		Prompt:        prompt,
		PromptHash:    promptHash,
		ImageSize:     imageSize,
		AspectRatio:   aspectRatio,
		Quality:       quality,
		Background:    background,
		ThinkingLevel: thinkingLevel,
		OutputCount:   outputCount,
		Sources:       sources,
		Mask:          mask,
		Fingerprint:   fingerprint,
	}, nil
}

// NormalizeCreativeImageInput checks nonempty bytes, the supplied cap, and
// short-signature agreement with MIME. Missing or unsupported headers are
// sniffed and JPG becomes JPEG. Returned bytes remain shared, and this
// signature check does not decode a complete image.
func NormalizeCreativeImageInput(input CreativeInputImage, maxBytes int64) (CreativeInputImage, error) {
	if len(input.Bytes) == 0 {
		return input, ErrCreativeInvalidMime
	}
	if int64(len(input.Bytes)) > maxBytes {
		return input, ErrCreativeAssetTooLarge
	}
	declaredMIME := strings.ToLower(strings.TrimSpace(input.Mime))
	mime := declaredMIME
	switch mime {
	case "image/png", "image/jpeg", "image/jpg", "image/webp":
	default:
		// Missing or unsupported header spelling cannot identify a usable image
		// label. Recognize known signatures first, then compare the chosen label
		// with the observed bytes before provider request construction.
		mime = SniffCreativeImageMime(input.Bytes)
		if mime == "" {
			return input, ErrCreativeInvalidMime
		}
	}
	// JPEG upload headers can use the legacy image/jpg spelling. Canonicalize it
	// before signature comparison so the returned label agrees with provider
	// builders and the recognized image/jpeg bytes.
	if mime == "image/jpg" {
		mime = "image/jpeg"
	}
	// A recognized MIME header still needs a matching short signature. This
	// prevents MIME-directed request encoding from choosing a label inconsistent
	// with the supplied leading image bytes.
	detectedMIME := SniffCreativeImageMime(input.Bytes)
	if detectedMIME == "" || detectedMIME != mime {
		return input, ErrCreativeInvalidMime
	}
	return CreativeInputImage{Bytes: input.Bytes, Mime: mime}, nil
}

// SniffCreativeImageMime delegates short PNG, JPEG, and WebP signature
// recognition for supplied bytes.
func SniffCreativeImageMime(data []byte) string { return upstream.SniffImageMIME(data) }

// CreativeImageDimensions reads dimensions with WebP config or registered
// PNG/JPEG config decoders. Unsupported MIME returns an error, and malformed
// headers retain decoder failures used by inpaint mask compatibility checks.
func CreativeImageDimensions(data []byte, mime string) (int, int, error) {
	if mime == "image/webp" {
		cfg, err := webp.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	}
	switch mime {
	case "image/png", "image/jpeg":
		// Blank imports register the JPEG and PNG decoders with
		// image.DecodeConfig.
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	default:
		return 0, 0, fmt.Errorf("unsupported image mime %s", mime)
	}
}

// BuildCreativeRequestFingerprint returns SHA-256 of the payload JSON or
// empty on encoding failure. Callers use normalized request values before
// comparing this digest within a user and workspace idempotency scope.
//
// Struct field order fixes the encoded control sequence, and source digest
// order remains part of request identity. Empty optional strings are omitted
// by their JSON tags; nonempty values participate in the digest. Source slice
// shape follows ImageSHA256 encoding, so validation supplies the canonical
// payload instead of treating arbitrarily shaped inputs as equivalent.
func BuildCreativeRequestFingerprint(payload CreativeFingerprintPayload) string {
	body, err := json.Marshal(payload)
	if err != nil {
		// No encoded request is available on failure. Return the empty
		// digest marker
		// rather than hashing absent bytes as if they represented normalized
		// submission identity.
		return ""
	}
	return Sha256Hex(body)
}

// CreativeImageHashes returns SHA-256 source digests in the supplied image
// order. Empty input produces an allocated empty slice, giving validated
// fingerprint payloads a consistent JSON array when generation has no source
// images.
func CreativeImageHashes(sources []CreativeInputImage) []string {
	hashes := make([]string, 0, len(sources))
	for i := range sources {
		hashes = append(hashes, Sha256Hex(sources[i].Bytes))
	}
	return hashes
}

// CreativeImageHash returns SHA-256 of image bytes or empty for a nil image
// pointer. A present image is hashed even when its bytes are empty, keeping
// absent mask input distinct in request fingerprint construction.
func CreativeImageHash(image *CreativeInputImage) string {
	if image == nil {
		return ""
	}
	return Sha256Hex(image.Bytes)
}

// Sha256Hex returns lowercase hexadecimal SHA-256 for supplied bytes used in
// creative request identity.
func Sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ModerateCreativeRequest reviews validated prompt and media using an OpenAI
// Images JSON shape with NoMediaRetention requested. Callers supply nonnil
// validated input when review is configured; absent review and service errors
// permit creation, while a successful nonnil denial blocks it.
//
// Source images keep their order and a supplied mask follows them in the
// temporary image list. MIME and base64 bytes share each data URL, so the
// moderation adapter sees the same submitted media identities used by the
// task. A configured nonblank request ID is reused; otherwise a fresh local ID
// identifies this review before hidden Key creation and reservation.
func (s *Public) ModerateCreativeRequest(ctx context.Context, userID int64, validated *ValidatedCreativeParams) error {
	if s.Moderation == nil {
		return nil
	}
	// Adapt ordered references and the optional mask to one temporary review list.
	// Each MIME label travels with its encoded bytes so moderation reads the
	// submitted media even though task persistence retains only their digests.
	images := make([]map[string]string, 0, len(validated.Sources)+1)
	for i := range validated.Sources {
		images = append(images, map[string]string{
			"image_url": "data:" + validated.Sources[i].Mime + ";base64," + base64.StdEncoding.EncodeToString(validated.Sources[i].Bytes),
		})
	}
	if validated.Mask != nil {
		images = append(images, map[string]string{
			"image_url": "data:" + validated.Mask.Mime + ";base64," + base64.StdEncoding.EncodeToString(validated.Mask.Bytes),
		})
	}
	payload := map[string]any{
		"model":  validated.Model,
		"prompt": validated.Prompt,
		"images": images,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	requestID := telemetry.NewRequestID()
	if s.RequestID != nil {
		if clientRequestID := strings.TrimSpace(s.RequestID(ctx)); clientRequestID != "" {
			requestID = clientRequestID
		}
	}
	decision, err := s.Moderation.Check(ctx, ModerationInput{
		RequestID:        requestID,
		UserID:           userID,
		BillingUserID:    userID,
		GroupID:          &validated.Group.ID,
		GroupName:        validated.Group.Name,
		Endpoint:         "/v1/creative/runs",
		Platform:         validated.Platform,
		Model:            validated.Model,
		Protocol:         "openai_images",
		Body:             body,
		NoMediaRetention: true,
	})
	if err != nil {
		// Service failure is logged and permits creation here. A
		// successful denied
		// decision still blocks below, keeping review availability
		// distinct from the
		// content permission returned for this temporary prompt and image body.
		s.warn("creative.moderation_check_failed",
			"user_id", userID,
			"error", err,
		)
		return nil
	}
	if decision != nil && !decision.Allowed {
		return ErrCreativeContentBlocked
	}
	return nil
}
