package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// media_codec.go parses Grok media inputs and translates image references and
// geometry for forwarding.
// Grok media callers need one request view for moderation, model rewriting and
// billing, even when input arrives as JSON or multipart form data. Parsing
// collects source URLs, uploads and masks, then applies injected image and video
// billing defaults; image forwarding separately converts supported edit inputs
// into xAI's JSON image-reference form.
//
// Geometry translation treats the client's size as a source for resolution and
// aspect ratio, with supplied supported resolution and nonblank aspect taking
// precedence over derived values. Exact supported dimension ratios win before
// nearest numeric-ratio selection. Multipart uploads retain only the configured
// read prefix and parsing keeps already-collected values when a later part
// fails. URL helpers distinguish a task's proxy-content path from the narrow
// signed-video host accepted for direct content download.

// These endpoint labels choose request method, body preparation and generation
// qualification; existing video lookups have separate status and content
// identities.
const (
	GrokMediaEndpointImagesGenerations GrokMediaEndpoint = "images_generations" // new image generation
	GrokMediaEndpointImagesEdits       GrokMediaEndpoint = "images_edits"       // source image editing
	GrokMediaEndpointVideosGenerations GrokMediaEndpoint = "videos_generations" // new video generation
	GrokMediaEndpointVideosEdits       GrokMediaEndpoint = "videos_edits"       // source video editing
	GrokMediaEndpointVideosExtensions  GrokMediaEndpoint = "videos_extensions"  // source video extension
	GrokMediaEndpointVideoStatus       GrokMediaEndpoint = "video_status"       // existing video status
	GrokMediaEndpointVideoContent      GrokMediaEndpoint = "video_content"      // existing video content

	// GrokMediaMaxEditSourceImages limits the combined parsed URL and upload
	// sources before image-edit forwarding constructs its xAI JSON request.
	GrokMediaMaxEditSourceImages = 3
)

// grokImagineAspectRatioValues supplies the labels and numeric ratios used for
// exact and nearest geometry selection; list order settles equal-distance choices.
// https://docs.x.ai/developers/model-capabilities/images/generation
var grokImagineAspectRatioValues = []struct {
	// label and ratio describe one supported geometry candidate: label is its
	// forwarded representation, and ratio supplies numeric distance comparison when
	// exact dimensions have no matching label.
	label string  // supported aspect ratio
	ratio float64 // numeric width/height ratio
}{
	{"1:1", 1},
	{"16:9", 16.0 / 9.0},
	{"9:16", 9.0 / 16.0},
	{"4:3", 4.0 / 3.0},
	{"3:4", 3.0 / 4.0},
	{"3:2", 1.5},
	{"2:3", 2.0 / 3.0},
	{"2:1", 2},
	{"1:2", 0.5},
	{"19.5:9", 19.5 / 9.0},
	{"9:19.5", 9.0 / 19.5},
	{"20:9", 20.0 / 9.0},
	{"9:20", 9.0 / 20.0},
	{"21:9", 21.0 / 9.0},
	{"5:2", 5.0 / 2.0},
}

// MediaNormalization supplies gateway upload limits and image/video billing
// normalization callbacks.
type MediaNormalization struct {
	// MaxUploadPartSize caps retained bytes per multipart part; nonpositive values
	// retain no part payload. Parsing does not report overflow when the retained
	// prefix is shorter than the source.
	MaxUploadPartSize int64  // multipart read bytes
	ImageTier1K       string // 1k billing tier label
	// MarshalJSON encodes reconstructed multipart edits; configure it before
	// PrepareGrokMediaForwardBody receives a multipart image-edit body so its
	// reconstructed fields can become one JSON request.
	MarshalJSON func(any) ([]byte, error)
	// NormalizeImageBillingTierOrDefault and
	// NormalizeVideoBillingResolutionOrDefault supply normalized image and video
	// billing fields during every ParseGrokMediaRequest call, including missing or
	// invalid supplied values. NormalizeImageBillingTierOrDefault: image billing
	// tier callback; NormalizeVideoBillingResolutionOrDefault: video billing
	// resolution callback
	NormalizeImageBillingTierOrDefault, NormalizeVideoBillingResolutionOrDefault func(string) string
	// NormalizeVideoBillingDurationSecondsOrDefault supplies the duration kept by
	// ParseGrokMediaRequest after either decoder, including image requests with no
	// supplied video duration. Configure it before parsing.
	NormalizeVideoBillingDurationSecondsOrDefault func(int) int
	// ClassifyImageBillingTier reports whether size has a billing tier before
	// geometry maps that tier into 1k or 2k. Configure it before deriving image
	// resolution from a size label.
	ClassifyImageBillingTier func(string) (string, bool)
	// ParseImageDimensions supplies pixel dimension candidates; aspect derivation
	// checks that both are positive before selecting an exact or approximate ratio.
	ParseImageDimensions func(string) (int, int, bool) // image dimensions parser
}
type (
	// MediaCodec applies injected normalization to Grok media request and response
	// forms.
	MediaCodec struct {
		Options MediaNormalization // injected media normalization
	}
	// GrokMediaEndpoint identifies the supported image or video operation for
	// forwarding.
	GrokMediaEndpoint string
)

// GrokMediaRequestInfo retains parsed request values for moderation, forwarding
// and billing.
type GrokMediaRequestInfo struct {
	Model  string // requested media model
	Prompt string // requested media prompt
	// N starts at one in ParseGrokMediaRequest; nonpositive parsed values select
	// one before moderation or forwarding uses the request view.
	N int // requested output count
	// Size and SizeTier retain supplied geometry input beside normalized image
	// billing classification. ParseGrokMediaRequest derives SizeTier through the
	// configured callback, including when Size is absent.
	Size        string // supplied image size
	SizeTier    string // normalized image billing tier
	AspectRatio string // supplied aspect ratio
	// ImageResolution retains canonical 1k/2k image values;
	// AssignGrokMediaResolution also retains the supplied label for video billing
	// because the same label can be used by either media endpoint.
	ImageResolution string // recognized image resolution
	// Resolution is normalized through the injected video callback even when image
	// requests provide no separate video resolution value.
	Resolution string // normalized video billing resolution
	// DurationSeconds is normalized through the injected duration callback after
	// either input format is parsed, including absent or invalid values.
	DurationSeconds int // seconds
	// InputImageURLs and Uploads contribute source images in that order to
	// moderation and edit forwarding. JSON image fields precede uploaded data URLs
	// when constructing the shared source list.
	InputImageURLs []string // parsed source-image URLs
	MaskImageURL   string   // parsed mask URL
	// Uploads retains each part's byte storage for later encoding; keep those
	// slices stable until moderation and forwarding finish consuming them.
	Uploads []upstream.ImageUpload // retained multipart image parts
	// MaskUpload takes precedence over MaskImageURL during edit preparation;
	// ModerationBody can include both mask sources when they are supplied.
	MaskUpload *upstream.ImageUpload // retained multipart mask
}

// RequiresRequestBody reports whether this endpoint uses a request payload.
func (e GrokMediaEndpoint) RequiresRequestBody() bool {
	return !e.IsVideoLookupRequest()
}

// IsVideoLookupRequest recognizes existing video status and content retrieval
// endpoints.
func (e GrokMediaEndpoint) IsVideoLookupRequest() bool {
	return e == GrokMediaEndpointVideoStatus || e == GrokMediaEndpointVideoContent
}

// IsGenerationRequest recognizes new image/video generation, editing and
// extension operations.
func (e GrokMediaEndpoint) IsGenerationRequest() bool {
	switch e {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits, GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideosEdits, GrokMediaEndpointVideosExtensions:
		return true
	default:
		return false
	}
}

// ModerationBody builds JSON containing the trimmed prompt and available image
// inputs. Source URLs precede image-labelled uploaded data, and mask sources
// follow. It returns nil when no moderation input remains or serialization
// fails; supplied upload bytes stay shared until encoding finishes.
func (r GrokMediaRequestInfo) ModerationBody() []byte {
	payload := map[string]any{}
	if prompt := strings.TrimSpace(r.Prompt); prompt != "" {
		payload["prompt"] = prompt
	}

	images := make([]map[string]string, 0, len(r.InputImageURLs)+len(r.Uploads)+1)
	for _, imageURL := range r.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range r.Uploads {
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if maskURL := strings.TrimSpace(r.MaskImageURL); maskURL != "" {
		images = append(images, map[string]string{"image_url": maskURL})
	}
	if r.MaskUpload != nil {
		if dataURL := r.MaskUpload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	// The moderation view is optional input. Encoding failure leaves no body for
	// that consumer instead of returning a partially encoded payload.
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

// HTTPMethod selects GET for video lookups and POST for other endpoint values.
func (e GrokMediaEndpoint) HTTPMethod() string {
	if e.IsVideoLookupRequest() {
		return http.MethodGet
	}
	return http.MethodPost
}

// ExtractGrokMediaModel returns the model from ParseGrokMediaRequest's
// normalized view. It uses the same configured callbacks as full parsing,
// including billing defaults; malformed multipart input can still yield values
// collected before parsing stops.
func (m MediaCodec) ExtractGrokMediaModel(contentType string, body []byte) string {
	return m.ParseGrokMediaRequest(contentType, body).Model
}

// ValidateGrokMediaRequest checks JSON or multipart Grok media payloads for
// malformed framing, duplicate object or scalar control fields, and case-variant
// protocol field names. Multipart requests require a nonblank boundary and
// continue to allow multiple image upload parts.
func ValidateGrokMediaRequest(contentType string, body []byte) error {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		boundary := strings.TrimSpace(params["boundary"])
		if boundary == "" {
			return fmt.Errorf("multipart boundary is required")
		}
		return validateGrokMediaMultipart(body, boundary)
	}
	return wirejson.ValidateRequestObject(body)
}

// validateGrokMediaMultipart inspects multipart Grok media parts and rejects
// malformed parts, duplicate mask uploads, and duplicate or case-variant scalar
// control fields. Each part is drained and closed while repeated image and
// reference_images uploads remain permitted.
func validateGrokMediaMultipart(body []byte, boundary string) error {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	seenScalars := make(map[string]struct{})
	maskSeen := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("invalid multipart body: %w", err)
		}
		name := strings.TrimSpace(part.FormName())
		fileName := strings.TrimSpace(part.FileName())
		_, copyErr := io.Copy(io.Discard, part)
		_ = part.Close()
		if copyErr != nil {
			return fmt.Errorf("invalid multipart part %q: %w", name, copyErr)
		}
		if name == "" {
			continue
		}
		folded := strings.ToLower(name)
		if fileName != "" {
			switch {
			case folded == "mask":
				if name != "mask" {
					return fmt.Errorf("ambiguous case variant %q for multipart field \"mask\"", name)
				}
				if maskSeen {
					return fmt.Errorf("duplicate multipart field: mask")
				}
				maskSeen = true
			case folded == "image" || strings.HasPrefix(folded, "image["):
				if !strings.HasPrefix(name, "image") {
					return fmt.Errorf("ambiguous case variant %q for multipart field \"image\"", name)
				}
			case folded == "reference_images" || strings.HasPrefix(folded, "reference_images["):
				if !strings.HasPrefix(name, "reference_images") {
					return fmt.Errorf("ambiguous case variant %q for multipart field \"reference_images\"", name)
				}
			}
			continue
		}
		switch folded {
		case "model", "prompt", "size", "aspect_ratio", "resolution",
			"duration", "n", "response_format", "quality", "frame_count",
			"frame_duration_ms", "moderation", "style", "output_format",
			"background", "mask", "image":
			if name != folded {
				return fmt.Errorf("ambiguous case variant %q for multipart field %q", name, folded)
			}
			if _, exists := seenScalars[name]; exists {
				return fmt.Errorf("duplicate multipart field: %s", name)
			}
			seenScalars[name] = struct{}{}
		}
	}
}

// ParseGrokMediaRequest collects JSON or multipart values and applies configured
// billing normalization. N starts at one and nonpositive counts return to one.
// Callers configure normalization callbacks; malformed multipart input yields
// its collected partial view, and returned uploads retain their byte storage.
func (m MediaCodec) ParseGrokMediaRequest(contentType string, body []byte) GrokMediaRequestInfo {
	info := GrokMediaRequestInfo{N: 1}
	// JSON validity selects field extraction independent of Content-Type; other
	// bytes try multipart parsing, whose partial view then receives the same
	// billing defaults.
	if gjson.ValidBytes(body) {
		m.ParseGrokMediaJSONRequest(body, &info)
	} else {
		m.ParseGrokMediaMultipartRequest(contentType, body, &info)
	}
	info.Model = strings.TrimSpace(info.Model)
	info.Prompt = strings.TrimSpace(info.Prompt)
	info.Size = strings.TrimSpace(info.Size)
	info.SizeTier = m.Options.NormalizeImageBillingTierOrDefault(info.Size)
	info.AspectRatio = strings.TrimSpace(info.AspectRatio)
	info.ImageResolution = m.GrokImagineImageResolution(info.ImageResolution)
	info.Resolution = m.Options.NormalizeVideoBillingResolutionOrDefault(info.Resolution)
	info.DurationSeconds = m.Options.NormalizeVideoBillingDurationSecondsOrDefault(info.DurationSeconds)
	if info.N <= 0 {
		info.N = 1
	}
	return info
}

// ParseGrokMediaJSONRequest assigns known JSON fields and appends recognized
// image URLs to info. Nil info is ignored. Image, images and reference_images
// contribute in that order; resolution is assigned to image or video fields
// according to its recognized form.
func (m MediaCodec) ParseGrokMediaJSONRequest(body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	info.Model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	info.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	info.Size = strings.TrimSpace(gjson.GetBytes(body, "size").String())
	info.AspectRatio = strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String())
	m.AssignGrokMediaResolution(strings.TrimSpace(gjson.GetBytes(body, "resolution").String()), info)
	if duration := gjson.GetBytes(body, "duration"); duration.Exists() && duration.Type == gjson.Number {
		// The billing view stores whole seconds when the JSON duration is numeric.
		info.DurationSeconds = int(duration.Int())
	}
	if n := gjson.GetBytes(body, "n"); n.Exists() && n.Type == gjson.Number {
		info.N = int(n.Int())
	}
	appendJSONImageURLs := func(value gjson.Result) {
		if !value.Exists() {
			return
		}
		switch {
		case value.IsArray():
			for _, item := range value.Array() {
				if imageURL := m.ExtractGrokMediaImageURL(item); imageURL != "" {
					info.InputImageURLs = append(info.InputImageURLs, imageURL)
				}
			}
		default:
			if imageURL := m.ExtractGrokMediaImageURL(value); imageURL != "" {
				info.InputImageURLs = append(info.InputImageURLs, imageURL)
			}
		}
	}
	appendJSONImageURLs(gjson.GetBytes(body, "image"))
	appendJSONImageURLs(gjson.GetBytes(body, "images"))
	appendJSONImageURLs(gjson.GetBytes(body, "reference_images"))
	info.MaskImageURL = m.ExtractGrokMediaImageURL(gjson.GetBytes(body, "mask"))
}

// ExtractGrokMediaImageURL reads a trimmed string or an image object's selected
// URL fields. Missing values return empty. Object extraction prefers url before
// the legacy image_url forms and leaves URL authorization to the request caller.
func (m MediaCodec) ExtractGrokMediaImageURL(value gjson.Result) string {
	if !value.Exists() {
		return ""
	}
	if value.Type == gjson.String {
		return strings.TrimSpace(value.String())
	}
	return m.GrokMediaJSONImageURL(value)
}

// GrokMediaJSONImageURL prefers a nonblank url before legacy image_url string or
// nested url. Its final legacy value is stringified by gjson, so this helper
// extracts candidates rather than validating a string-only image object.
func (m MediaCodec) GrokMediaJSONImageURL(value gjson.Result) string {
	if imageURL := strings.TrimSpace(value.Get("url").String()); imageURL != "" {
		return imageURL
	}
	if nested := value.Get("image_url"); nested.Exists() {
		if nested.Type == gjson.String {
			return strings.TrimSpace(nested.String())
		}
		if imageURL := strings.TrimSpace(nested.Get("url").String()); imageURL != "" {
			return imageURL
		}
	}
	return strings.TrimSpace(value.Get("image_url").String())
}

// GrokMediaImageObject creates the url/type fields used in xAI image references.
func (m MediaCodec) GrokMediaImageObject(imageURL string) map[string]string {
	return map[string]string{"url": imageURL, "type": "image_url"}
}

// ParseGrokMediaMultipartRequest fills info from recognized form fields and
// image or mask uploads. Nil info, invalid MIME type or missing boundary stops
// parsing. Each part read is capped and closed; errors retain prior values, and
// oversized parts retain a prefix rather than reporting overflow.
func (m MediaCodec) ParseGrokMediaMultipartRequest(contentType string, body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return
	}
	// Multipart parsing is best effort: each part is closed after inspection, and
	// malformed or unreadable later parts leave previously collected values
	// available.
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		// Retain at most the configured byte prefix for each part. Closing then
		// advances parsing, so oversized uploads do not receive an overflow error.
		data, err := io.ReadAll(io.LimitReader(part, m.Options.MaxUploadPartSize))
		_ = part.Close()
		if err != nil {
			return
		}
		fileName := strings.TrimSpace(part.FileName())
		partContentType := strings.TrimSpace(part.Header.Get("Content-Type"))
		if fileName != "" {
			upload := upstream.ImageUpload{
				FieldName:   name,
				FileName:    fileName,
				ContentType: partContentType,
				Data:        data,
			}
			if name == "mask" {
				info.MaskUpload = &upload
				continue
			}
			if name == "image" || strings.HasPrefix(name, "image[") {
				info.Uploads = append(info.Uploads, upload)
			}
			continue
		}

		value := strings.TrimSpace(string(data))
		switch name {
		case "model":
			info.Model = value
		case "prompt":
			info.Prompt = value
		case "size":
			info.Size = value
		case "aspect_ratio":
			info.AspectRatio = value
		case "resolution":
			m.AssignGrokMediaResolution(value, info)
		// Invalid duration or n text leaves its previously collected value intact;
		// parsing continues so later form fields remain available to the request view.
		case "duration":
			if duration, err := strconv.Atoi(value); err == nil {
				info.DurationSeconds = duration
			}
		case "n":
			if n, err := strconv.Atoi(value); err == nil {
				info.N = n
			}
		case "image", "image_url":
			if value != "" {
				info.InputImageURLs = append(info.InputImageURLs, value)
			}
		case "mask", "mask_image_url":
			info.MaskImageURL = value
		}
	}
}

// RewriteGrokMediaRequestModel delegates JSON or multipart model replacement to
// the shared image rewriter. The returned content type carries any rebuilt
// multipart boundary; callers pass it with the rewritten bytes to the request
// builder.
func (m MediaCodec) RewriteGrokMediaRequestModel(body []byte, contentType, model string) ([]byte, string, error) {
	return upstream.RewriteImageModel(body, contentType, model)
}

// GrokMediaSignedVideoContentURL selects video.url for signed download or
// returns empty for this task's proxy-content path. Other nonempty URLs need
// HTTPS vidgen.x.ai with no userinfo and no port other than 443. Callers still
// apply their download policy to the accepted target.
func (m MediaCodec) GrokMediaSignedVideoContentURL(body []byte, requestID string) (string, error) {
	rawURL := strings.TrimSpace(gjson.GetBytes(body, "video.url").String())
	if rawURL == "" {
		return "", nil
	}
	// A task proxy-content path uses the selected provider's base and credentials.
	// Return empty here so the caller reconstructs that authenticated relay target.
	if m.IsGrokMediaVideoContentURL(rawURL, requestID) {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Hostname(), "vidgen.x.ai") ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.User != nil {
		return "", fmt.Errorf("grok media status returned an unsupported video content URL")
	}
	return parsed.String(), nil
}

// IsGrokCLIProxyTarget recognizes the CLI proxy hostname regardless of port or
// path.
func (m MediaCodec) IsGrokCLIProxyTarget(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "cli-chat-proxy.grok.com")
}

// PrepareGrokMediaForwardBody converts image-edit input to the supported JSON
// reference form. Other endpoints return supplied body and content type. JSON
// edits use image-reference rewriting; multipart edits need configured parsing
// and MarshalJSON callbacks.
//
// Collected URLs precede uploaded data URLs in one source-image list, whose
// combined count is checked against the edit limit. The first image also
// occupies image, and multiple sources occupy images. An uploaded mask
// supersedes a mask URL. The reconstructed JSON includes selected parsed fields;
// caller-supplied multipart metadata outside that view does not reach this body.
func (m MediaCodec) PrepareGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if endpoint != GrokMediaEndpointImagesEdits {
		return body, contentType, nil
	}
	if gjson.ValidBytes(body) {
		out, err := m.NormalizeGrokMediaJSONImageRefs(body)
		return out, contentType, err
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return body, contentType, nil
	}

	info := m.ParseGrokMediaRequest(contentType, body)
	payload := make(map[string]any)
	if info.Model != "" {
		payload["model"] = info.Model
	}
	if info.Prompt != "" {
		payload["prompt"] = info.Prompt
	}
	if info.N > 1 {
		payload["n"] = info.N
	}
	if info.Size != "" {
		payload["size"] = info.Size
	}
	if info.ImageResolution != "" {
		payload["resolution"] = info.ImageResolution
	}
	if info.AspectRatio != "" {
		payload["aspect_ratio"] = info.AspectRatio
	}

	// Combine URL sources and encoded uploads before enforcing the edit-image
	// count; both formats consume entries in the same forwarded request.
	images := make([]map[string]string, 0, len(info.InputImageURLs)+len(info.Uploads))
	for _, imageURL := range info.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, m.GrokMediaImageObject(imageURL))
		}
	}
	for _, upload := range info.Uploads {
		dataURL, err := upstream.ImageUploadToDataURL(upload)
		if err != nil {
			return nil, "", err
		}
		images = append(images, m.GrokMediaImageObject(dataURL))
	}
	if len(images) > GrokMediaMaxEditSourceImages {
		return nil, "", fmt.Errorf("a maximum of %d source images is supported for image edits", GrokMediaMaxEditSourceImages)
	}
	if len(images) > 0 {
		payload["image"] = images[0]
		if len(images) > 1 {
			payload["images"] = images
		}
	}

	maskImageURL := strings.TrimSpace(info.MaskImageURL)
	// An uploaded mask supplies concrete bytes for this edit and replaces a
	// separately provided mask URL in the reconstructed JSON body.
	if info.MaskUpload != nil {
		dataURL, err := upstream.ImageUploadToDataURL(*info.MaskUpload)
		if err != nil {
			return nil, "", err
		}
		maskImageURL = dataURL
	}
	if maskImageURL != "" {
		payload["mask"] = m.GrokMediaImageObject(maskImageURL)
	}

	out, err := m.Options.MarshalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	return out, "application/json", nil
}

// NormalizeGrokMediaJSONImageRefs checks the parsed edit source count, then
// rewrites image, images and mask. reference_images participates in parsing and
// the count but is not rewritten by this helper. The configured request
// normalizers are required for its parsing step.
func (m MediaCodec) NormalizeGrokMediaJSONImageRefs(body []byte) ([]byte, error) {
	info := m.ParseGrokMediaRequest("application/json", body)
	// Count every parsed source before field rewriting, including reference_images
	// entries that this helper leaves in their supplied representation.
	if len(info.InputImageURLs) > GrokMediaMaxEditSourceImages {
		return nil, fmt.Errorf("a maximum of %d source images is supported for image edits", GrokMediaMaxEditSourceImages)
	}
	out := body
	var err error
	for _, field := range []string{"image", "images", "mask"} {
		out, err = m.RewriteGrokMediaJSONImageField(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RewriteGrokMediaJSONImageField replaces a URL-bearing value or array with xAI
// image objects containing url and type. A missing field or unusable scalar
// returns the supplied bytes, sharing their storage with body.
//
// Arrays retain reference order, but every entry needs an extracted URL before
// the field can be replaced. One unusable entry keeps the whole original field
// so conversion does not silently shorten the source list. Successful rewriting
// drops other object fields and reports serialization errors.
func (m MediaCodec) RewriteGrokMediaJSONImageField(body []byte, path string) ([]byte, error) {
	value := gjson.GetBytes(body, path)
	if !value.Exists() {
		return body, nil
	}
	if value.IsArray() {
		rewritten := make([]map[string]string, 0, len(value.Array()))
		for _, item := range value.Array() {
			imageURL := m.ExtractGrokMediaImageURL(item)
			// Keep the whole supplied field when an array entry lacks a usable reference;
			// rebuilding a partial list would discard that entry.
			if imageURL == "" {
				return body, nil
			}
			rewritten = append(rewritten, m.GrokMediaImageObject(imageURL))
		}
		out, err := sjson.SetBytes(body, path, rewritten)
		if err != nil {
			return nil, fmt.Errorf("rewrite grok media %s: %w", path, err)
		}
		return out, nil
	}
	imageURL := m.ExtractGrokMediaImageURL(value)
	if imageURL == "" {
		return body, nil
	}
	out, err := sjson.SetBytes(body, path, m.GrokMediaImageObject(imageURL))
	if err != nil {
		return nil, fmt.Errorf("rewrite grok media %s: %w", path, err)
	}
	return out, nil
}

// NormalizeGrokMediaForwardBody canonicalizes selected image URL fields in JSON
// request bodies. Image edits select image/images/mask; video generation also
// selects reference_images. Lookup or non-JSON bodies pass through with their
// supplied content type, and other endpoints select no image fields.
func (m MediaCodec) NormalizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !endpoint.RequiresRequestBody() || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	var imageFields []string
	switch endpoint {
	case GrokMediaEndpointImagesEdits:
		imageFields = []string{"image", "images", "mask"}
	case GrokMediaEndpointVideosGenerations:
		imageFields = []string{"image", "images", "reference_images"}
	}
	var err error
	body, err = m.CanonicalizeGrokMediaImageURLFields(body, imageFields...)
	if err != nil {
		return nil, "", err
	}
	return body, contentType, nil
}

// CanonicalizeGrokMediaImageURLFields normalizes legacy URL keys in the selected
// object or array paths. Missing fields pass through. Each array element is
// handled independently, and errors stop the operation before returning any
// partially rewritten body.
func (m MediaCodec) CanonicalizeGrokMediaImageURLFields(body []byte, fields ...string) ([]byte, error) {
	out := body
	for _, field := range fields {
		value := gjson.GetBytes(out, field)
		if !value.Exists() {
			continue
		}
		if value.IsArray() {
			for index := range value.Array() {
				var err error
				out, err = m.CanonicalizeGrokMediaImageURLObject(out, fmt.Sprintf("%s.%d", field, index))
				if err != nil {
					return nil, err
				}
			}
			continue
		}
		var err error
		out, err = m.CanonicalizeGrokMediaImageURLObject(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CanonicalizeGrokMediaImageURLObject copies legacy image_url into a blank url
// and deletes the legacy key. A nonblank url takes precedence, and a missing
// legacy field returns the supplied bytes.
//
// The selected value is installed before its legacy key is removed, keeping the
// reference available across the change of field name. The decoded legacy JSON
// value can retain non-string shapes; this normalization does not establish a
// valid image reference. Callers apply any required shape and target validation
// before using the rewritten object.
func (m MediaCodec) CanonicalizeGrokMediaImageURLObject(body []byte, path string) ([]byte, error) {
	legacyPath := path + ".image_url"
	legacy := gjson.GetBytes(body, legacyPath)
	if !legacy.Exists() {
		return body, nil
	}

	out := body
	// Prefer an existing nonblank url; otherwise copy the legacy value before
	// deleting its key so the selected reference survives normalization.
	if strings.TrimSpace(gjson.GetBytes(out, path+".url").String()) == "" {
		var err error
		out, err = sjson.SetBytes(out, path+".url", legacy.Value())
		if err != nil {
			return nil, fmt.Errorf("normalize grok media image url: %w", err)
		}
	}
	out, err := sjson.DeleteBytes(out, legacyPath)
	if err != nil {
		return nil, fmt.Errorf("remove legacy grok media image url: %w", err)
	}
	return out, nil
}

// SanitizeGrokMediaForwardBody applies image geometry translation to JSON
// generation or edit bodies. Lookup, non-JSON and other media operations pass
// through. Geometry errors return a contextual error before the upstream request
// can be built.
func (m MediaCodec) SanitizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !endpoint.RequiresRequestBody() || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		out, err := m.ApplyGrokImagineImageGeometry(body)
		if err != nil {
			return nil, "", fmt.Errorf("sanitize grok media size: %w", err)
		}
		return out, contentType, nil
	default:
		return body, contentType, nil
	}
}

// ExtractGrokMediaVideoRequestID selects a task identifier from valid JSON
// response fields. request_id/id forms precede task_id aliases across top-level,
// data and video objects. Missing, invalid or unmatched bodies return empty for
// the caller's ownership decision.
func (m MediaCodec) ExtractGrokMediaVideoRequestID(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	// The ownership path depends on a stable identifier selection. Keep request_id
	// and id forms ahead of task_id aliases when a response contains several
	// values.
	for _, path := range []string{"request_id", "id", "data.request_id", "data.id", "video.request_id", "video.id", "task_id", "data.task_id", "video.task_id"} {
		if id := strings.TrimSpace(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

// IsGrokMediaVideoContentURL matches the trailing videos/requestID/content path
// after decoding its identifier segment. It checks path identity rather than
// origin, scheme or host; callers use the result to reconstruct the selected
// provider's authenticated proxy URL.
func (m MediaCodec) IsGrokMediaVideoContentURL(rawURL, requestID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 3 {
		return false
	}
	requestID = strings.Trim(requestID, "/")
	decodedID, err := url.PathUnescape(segments[len(segments)-2])
	if err != nil {
		return false
	}
	return segments[len(segments)-3] == "videos" &&
		decodedID == requestID &&
		segments[len(segments)-1] == "content"
}

// ApplyGrokImagineImageGeometry translates size into upstream image resolution
// and aspect fields. It starts with copied bytes and removes size from the
// result when present. A recognized explicit resolution is canonicalized;
// otherwise size can derive its replacement.
//
// A nonblank aspect remains supplied, while a missing aspect can derive from
// dimensions. If no derivation succeeds, existing unrecognized resolution text
// can remain. This precedence lets explicit geometry coexist with billing size
// labels, and the result uses xAI's geometry fields while returning JSON-edit
// errors before forwarding.
func (m MediaCodec) ApplyGrokImagineImageGeometry(body []byte) ([]byte, error) {
	size := strings.TrimSpace(gjson.GetBytes(body, "size").String())
	resolution := m.GrokImagineImageResolution(gjson.GetBytes(body, "resolution").String())
	aspect := strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String())
	out := append([]byte(nil), body...)

	// A recognized explicit resolution takes precedence over size derivation; an
	// absent recognized value allows the billing size to select upstream geometry.
	if resolution == "" {
		if derived := m.GrokImagineImageResolutionFromSize(size); derived != "" {
			next, err := sjson.SetBytes(out, "resolution", derived)
			if err != nil {
				return nil, err
			}
			out = next
		}
	} else if gjson.GetBytes(body, "resolution").String() != resolution {
		next, err := sjson.SetBytes(out, "resolution", resolution)
		if err != nil {
			return nil, err
		}
		out = next
	}

	// Derive aspect only when no nonblank aspect was supplied, letting
	// caller-selected geometry survive independent image-size normalization.
	if aspect == "" {
		if derived := m.GrokImagineAspectRatioFromSize(size); derived != "" {
			next, err := sjson.SetBytes(out, "aspect_ratio", derived)
			if err != nil {
				return nil, err
			}
			out = next
		}
	}

	if !gjson.GetBytes(out, "size").Exists() {
		return out, nil
	}
	// Upstream geometry uses resolution and aspect fields; remove the size label
	// after their derivation so billing input does not remain an image parameter.
	return sjson.DeleteBytes(out, "size")
}

// AssignGrokMediaResolution retains video labels and recognizes image geometry.
// Nil info or blank text is ignored. A 1k/2k value populates both interpretations,
// allowing the selected endpoint to consume its own dimensions without losing
// a custom video resolution during shared request parsing.
func (m MediaCodec) AssignGrokMediaResolution(value string, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	info.Resolution = value
	if img := m.GrokImagineImageResolution(value); img != "" {
		info.ImageResolution = img
	}
}

// GrokImagineImageResolution canonicalizes trimmed case-insensitive 1k or 2k
// image resolution. Other values return empty, allowing callers to derive
// geometry from size or retain the value for separate video-resolution
// normalization.
func (m MediaCodec) GrokImagineImageResolution(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1k":
		return "1k"
	case "2k":
		return "2k"
	default:
		return ""
	}
}

// GrokImagineImageResolutionFromSize accepts an explicit image tier or delegates
// size classification. Unclassified size returns empty. The configured
// ImageTier1K maps to 1k; every other successfully classified billing tier maps
// to 2k.
func (m MediaCodec) GrokImagineImageResolutionFromSize(size string) string {
	if explicit := m.GrokImagineImageResolution(size); explicit != "" {
		return explicit
	}
	tier, ok := m.Options.ClassifyImageBillingTier(size)
	if !ok {
		return ""
	}
	if tier == m.Options.ImageTier1K {
		return "1k"
	}
	return "2k"
}

// GrokImagineAspectRatioFromSize selects a supported ratio from configured
// dimension parsing. Invalid or nonpositive dimensions return empty. A reduced
// integer ratio matching the supported labels returns that exact form before
// approximation.
//
// Otherwise selection minimizes absolute numeric width/height distance to the
// fixed candidates. Equal distances keep the earlier candidate because
// replacement requires a strictly smaller delta. The chosen label is therefore a
// supported approximation for valid dimensions, and callers can retain a
// supplied aspect when they want to avoid this derivation.
func (m MediaCodec) GrokImagineAspectRatioFromSize(size string) string {
	width, height, ok := m.Options.ParseImageDimensions(strings.TrimSpace(size))
	if !ok || width <= 0 || height <= 0 {
		return ""
	}
	div := m.GrokImagineGCD(width, height)
	exact := strconv.Itoa(width/div) + ":" + strconv.Itoa(height/div)
	for _, candidate := range grokImagineAspectRatioValues {
		if candidate.label == exact {
			return exact
		}
	}
	// After exact label matching fails, minimize numeric aspect distance; strict
	// improvement keeps the first supported candidate when distances tie.
	ratio := float64(width) / float64(height)
	bestLabel := ""
	bestDelta := math.MaxFloat64
	for _, candidate := range grokImagineAspectRatioValues {
		delta := math.Abs(ratio - candidate.ratio)
		if delta < bestDelta {
			bestDelta = delta
			bestLabel = candidate.label
		}
	}
	return bestLabel
}

// GrokImagineGCD computes an integer divisor for reducing image dimensions.
// Geometry callers supply positive pixel widths and heights; a zero divisor
// becomes one before division. Other signed inputs follow Go integer negation.
func (m MediaCodec) GrokImagineGCD(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	// A zero pair would yield a zero divisor; return one so callers can keep
	// dimension reduction defined for that degenerate input.
	if a == 0 {
		return 1
	}
	return a
}

// NormalizeImagineAspectRatio accepts trimmed supported labels or the exact auto
// value. Blank and unmatched values return empty so callers can leave aspect
// unspecified and use their upstream default; label comparison remains
// case-sensitive.
func NormalizeImagineAspectRatio(aspectRatio string) string {
	aspectRatio = strings.TrimSpace(aspectRatio)
	if aspectRatio == "" {
		return ""
	}
	if aspectRatio == "auto" {
		return aspectRatio
	}
	for _, candidate := range grokImagineAspectRatioValues {
		if candidate.label == aspectRatio {
			return aspectRatio
		}
	}
	return ""
}
