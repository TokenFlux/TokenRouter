package upstream

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// image_request_parse.go parses image generation and editing requests into
// forwarding, moderation, and routing evidence.
// Image generation and editing share request options but receive reference
// images in different encodings. JSON editing uses image_url entries, while
// multipart editing retains named file parts and their byte content. Both
// paths record whether model and size were supplied before defaults, giving
// gateway capability selection evidence that a defaulted model alone cannot
// provide.
//
// The normalized request retains its original body storage for forwarding and
// builds separate prompt/image JSON for moderation. JSON numeric options use
// integer conversion after a number-type check; null animation controls remain
// absent until GIF defaults are applied. Multipart text uses integer parsing
// and caps each named part at twenty MiB, returning its prefix when larger.
// Parsing collects request evidence and defaults, while model-family
// validation, billing size tiers, and provider capability selection are
// performed by gateway consumers.

// These endpoint paths and the per-part read bound select supported image
// request forms and limit retained multipart payload storage.
const (
	OpenAIImagesGenerationsEndpoint = "/v1/images/generations" // image generation endpoint path
	OpenAIImagesEditsEndpoint       = "/v1/images/edits"       // image editing endpoint path
	OpenAIImageMaxUploadPartSize    = 20 << 20                 // 20 MiB per named multipart part
)

// ImageRequest retains parsed image options and shared input storage for
// gateway consumers.
type ImageRequest struct {
	Endpoint    string // normalized image endpoint path
	ContentType string // trimmed input content type
	Multipart   bool   // multipart input encoding indicator
	// Model carries the selected or defaulted name, while ExplicitModel records
	// whether the parser-selected input was nonblank. Later multipart model
	// fields replace earlier values. Gateway capability checks consume that
	// supplied-value distinction.
	Model         string // parsed or supplied model
	ExplicitModel bool   // nonblank model selection observed
	Prompt        string // trimmed supplied image prompt
	Stream        bool   // requested image streaming mode
	N             int    // requested image output count
	// Size and ExplicitSize retain the supplied size spelling and its nonblank
	// presence. Gateway capability selection uses ExplicitSize independently of
	// billing tier normalization and the chosen image model.
	Size         string // supplied image size spelling
	ExplicitSize bool   // nonblank size selection observed
	// SizeTier is assigned by gateway/media using billing size normalization.
	// Native parsing leaves it empty while retaining Size for capability selection.
	SizeTier       string // normalized image billing tier
	ResponseFormat string // lowercase response encoding selection
	Quality        string // supplied image quality setting
	Background     string // supplied image background setting
	OutputFormat   string // supplied output image format
	Moderation     string // supplied image moderation setting
	InputFidelity  string // supplied input fidelity setting
	Style          string // supplied image style setting
	// FrameCount and FrameDurationMS retain optional animation controls. JSON null
	// and omitted fields remain nil until NormalizeGIF supplies defaults for GIF
	// output; static requests leave both fields unset.
	FrameCount      *int // requested animation frame count
	FrameDurationMS *int // milliseconds per animation frame
	// OutputCompression and PartialImages retain optional integer settings, with
	// nil representing absence. Gateway request conversion shares both pointers;
	// copy their values before independent mutation. Their ranges are not validated here.
	OutputCompression *int // requested output compression percentage
	PartialImages     *int // requested partial image count
	// HasMask records mask-field presence in JSON, including null, or a nonempty
	// multipart mask upload. Gateway capability selection consumes this presence flag.
	HasMask bool // input mask presence evidence
	// HasNativeOptions records option presence for gateway capability selection.
	// JSON considers field existence. Multipart accepts empty string-option
	// values as present; integer options require successful parsing, and
	// case-normalized unhandled names count only with nonblank values.
	HasNativeOptions bool // native image option evidence
	// InputImageURLs appends nonblank JSON edit references in source order. Gateway
	// request conversion shares this slice, so keep elements stable during reads.
	InputImageURLs []string // shared edit reference URLs
	MaskImageURL   string   // supplied edit mask URL
	// Uploads retains multipart image parts and their separately read byte payloads.
	// Gateway request conversion shares the slice and each upload's Data storage.
	Uploads []ImageUpload // retained multipart image uploads
	// MaskUpload retains the last nonempty multipart mask file. Gateway request
	// conversion shares this pointer and its Data bytes, so copy before
	// independent mutation.
	MaskUpload *ImageUpload // retained multipart mask upload
	// Body borrows the original input slice supplied to ParseImageRequest.
	// Keep its bytes stable while forwarding or retained request consumers use it.
	Body     []byte // shared original request bytes
	BodyHash string // hexadecimal truncated body SHA-256
}

// ModerationBody returns new JSON bytes containing a trimmed prompt, nonblank
// URL references, and encoded image uploads. Nil requests, empty moderation
// content, or marshaling errors return nil; upload data URLs come from
// ModerationDataURL.
func (r *ImageRequest) ModerationBody() []byte {
	if r == nil {
		return nil
	}
	payload := map[string]any{}
	if prompt := strings.TrimSpace(r.Prompt); prompt != "" {
		payload["prompt"] = prompt
	}
	images := r.moderationImages()
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Moderation callers interpret nil as absent extracted content. Keep the
		// same result on encoding failure rather than returning a partial
		// moderation payload.
		return nil
	}
	return body
}

// moderationImages collects URL references, upload data URLs, and mask inputs
// in that order. Nil requests return nil; blank URLs and uploads producing an
// empty ModerationDataURL are omitted from the list. URL syntax is not
// validated here.
func (r *ImageRequest) moderationImages() []map[string]string {
	if r == nil {
		return nil
	}
	images := make([]map[string]string, 0, len(r.InputImageURLs)+len(r.Uploads)+1)
	for _, imageURL := range r.InputImageURLs {
		imageURL = strings.TrimSpace(imageURL)
		if imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	// Upload and mask data URLs use the image-only moderation encoder. Omit
	// an empty encoder result while keeping nonblank URL references in the
	// collected list.
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
	return images
}

// IsEdits reports whether Endpoint exactly equals the normalized image-edit
// path. Nil requests return false; callers normalize the endpoint before using
// this predicate to choose reference-image parsing or capability selection.
func (r *ImageRequest) IsEdits() bool {
	return r != nil && r.Endpoint == OpenAIImagesEditsEndpoint
}

// StickySessionSeed joins trimmed endpoint, model, size, and prompt values
// with an image-domain prefix for routing affinity. Nil requests return empty;
// the fields are joined with unescaped delimiters.
//
// When prompt is blank and BodyHash is nonempty, its truncated body digest is
// appended so promptless payload differences can affect the seed. Other
// request options enter this seed only through that condition. Consumers treat
// the value as a routing hint because unescaped components and finite digest
// size permit collisions.
func (r *ImageRequest) StickySessionSeed() string {
	if r == nil {
		return ""
	}
	parts := []string{
		"openai-images",
		strings.TrimSpace(r.Endpoint),
		strings.TrimSpace(r.Model),
		strings.TrimSpace(r.Size),
		strings.TrimSpace(r.Prompt),
	}
	seed := strings.Join(parts, "|")
	// A promptless request still needs payload-sensitive routing input. Append
	// the retained body digest in that case; prompt-bearing requests use their
	// selected endpoint, model, size, and prompt fields as the routing input.
	if strings.TrimSpace(r.Prompt) == "" && r.BodyHash != "" {
		seed += "|body=" + r.BodyHash
	}
	return seed
}

// ParseOpenAIImagesJSONRequest fills a nonnil request from JSON paths after
// the caller has selected its endpoint. It records supplied model and size
// presence before defaults, appends edit image references, and can leave
// partial updates when validation fails.
//
// Stream requires a JSON boolean, while n, compression, and preview controls
// require numbers and then use gjson integer conversion, including fractional
// truncation. Animation controls accept integers or null; null leaves the
// setting unspecified for later GIF defaults. Editing requires an image_url
// reference; file_id-only images and any mask.file_id are rejected. This helper
// performs path extraction, leaving whole-document validation and defaults to
// ParseImageRequest.
func ParseOpenAIImagesJSONRequest(body []byte, req *ImageRequest) error {
	if err := ValidateGIFJSONControls(body); err != nil {
		return err
	}
	// Model and size presence is recorded before defaults so gateway capability
	// selection can distinguish a client-selected value from the parser's default model.
	if modelResult := gjson.GetBytes(body, "model"); modelResult.Exists() {
		req.Model = strings.TrimSpace(modelResult.String())
		req.ExplicitModel = req.Model != ""
	}
	req.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())

	if streamResult := gjson.GetBytes(body, "stream"); streamResult.Exists() {
		if streamResult.Type != gjson.True && streamResult.Type != gjson.False {
			return fmt.Errorf("invalid stream field type")
		}
		req.Stream = streamResult.Bool()
	}

	if nResult := gjson.GetBytes(body, "n"); nResult.Exists() {
		if nResult.Type != gjson.Number {
			return fmt.Errorf("invalid n field type")
		}
		// Numeric JSON counts use integer conversion, which truncates fractions
		// before the positive-count check; multipart counts instead use integer
		// text parsing.
		req.N = int(nResult.Int())
		if req.N <= 0 {
			return fmt.Errorf("n must be greater than 0")
		}
	}

	if sizeResult := gjson.GetBytes(body, "size"); sizeResult.Exists() {
		req.Size = strings.TrimSpace(sizeResult.String())
		req.ExplicitSize = req.Size != ""
	}
	req.ResponseFormat = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "response_format").String()))
	req.Quality = strings.TrimSpace(gjson.GetBytes(body, "quality").String())
	req.Background = strings.TrimSpace(gjson.GetBytes(body, "background").String())
	req.OutputFormat = strings.TrimSpace(gjson.GetBytes(body, "output_format").String())
	req.Moderation = strings.TrimSpace(gjson.GetBytes(body, "moderation").String())
	req.InputFidelity = strings.TrimSpace(gjson.GetBytes(body, "input_fidelity").String())
	req.Style = strings.TrimSpace(gjson.GetBytes(body, "style").String())
	req.HasMask = gjson.GetBytes(body, "mask").Exists()
	// Compression and partial-image controls retain integer-converted JSON numbers.
	// Their ranges remain for upstream validation, and nil distinguishes
	// omitted controls.
	if outputCompression := gjson.GetBytes(body, "output_compression"); outputCompression.Exists() {
		if outputCompression.Type != gjson.Number {
			return fmt.Errorf("invalid output_compression field type")
		}
		v := int(outputCompression.Int())
		req.OutputCompression = &v
	}
	if partialImages := gjson.GetBytes(body, "partial_images"); partialImages.Exists() {
		if partialImages.Type != gjson.Number {
			return fmt.Errorf("invalid partial_images field type")
		}
		v := int(partialImages.Int())
		req.PartialImages = &v
	}
	for _, field := range []struct {
		name   string // numeric animation field name
		target **int  // parsed optional value destination
	}{{"frame_count", &req.FrameCount}, {"frame_duration_ms", &req.FrameDurationMS}} {
		raw := gjson.GetBytes(body, field.name)
		// Native Images accepts null animation controls. Preserve their absent state
		// so GIF normalization supplies defaults without adding animation to static requests.
		if raw.Exists() && raw.Type != gjson.Null {
			number, err := strconv.Atoi(raw.Raw)
			if raw.Type != gjson.Number || err != nil {
				return fmt.Errorf("%s must be an integer", field.name)
			}
			*field.target = &number
		}
	}
	if req.IsEdits() {
		images := gjson.GetBytes(body, "images")
		if images.Exists() {
			if !images.IsArray() {
				return fmt.Errorf("invalid images field type")
			}
			// Editing references use URLs in this representation. A nonblank image_url
			// takes precedence over file_id, whose unsupported form is checked only
			// when no nonblank URL was appended for that item.
			for _, item := range images.Array() {
				if imageURL := strings.TrimSpace(item.Get("image_url").String()); imageURL != "" {
					req.InputImageURLs = append(req.InputImageURLs, imageURL)
					continue
				}
				if item.Get("file_id").Exists() {
					return fmt.Errorf("images[].file_id is not supported (use images[].image_url instead)")
				}
			}
		}
		if maskImageURL := strings.TrimSpace(gjson.GetBytes(body, "mask.image_url").String()); maskImageURL != "" {
			req.MaskImageURL = maskImageURL
			req.HasMask = true
		}
		// A mask file_id cannot be resolved by this URL-based editing path.
		// Reject its presence even when a mask image_url also appears in the request.
		if gjson.GetBytes(body, "mask.file_id").Exists() {
			return fmt.Errorf("mask.file_id is not supported (use mask.image_url instead)")
		}
		if len(req.InputImageURLs) == 0 {
			return fmt.Errorf("images[].image_url is required")
		}
	}
	req.HasNativeOptions = HasOpenAINativeImageOptions(func(path string) bool {
		return gjson.GetBytes(body, path).Exists()
	})
	return nil
}

// ParseOpenAIImagesMultipartRequest fills a nonnil request from named
// multipart parts using the supplied boundary. It leaves partial updates on
// error, appends image uploads, and lets the last recognized scalar field or
// nonempty mask upload replace its earlier value.
//
// Each named part retains at most twenty MiB; oversized files and scalar
// fields can therefore be truncated without an oversize error. Unknown fields
// are ignored, option presence is retained for capability selection, and
// editing requires at least one image upload. Multipart transfer decoding
// precedes part reads, and upload dimensions remain zero through
// ParseOpenAIImageDimensions.
func ParseOpenAIImagesMultipartRequest(body []byte, contentType string, req *ImageRequest) error {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("invalid multipart content-type: %w", err)
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return fmt.Errorf("multipart boundary is required")
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	gifSeen := false
	controls := map[string]int{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read multipart body: %w", err)
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			// Unnamed parts supply no request field. Close them so their unread
			// payload is drained before advancing to the next named form part.
			_ = part.Close()
			continue
		}

		// Retain a bounded prefix of every named part, including scalar fields.
		// Closing the part drains its unread suffix before the next part; this
		// cap does not report oversize and close errors do not replace read errors.
		data, err := io.ReadAll(io.LimitReader(part, OpenAIImageMaxUploadPartSize))
		_ = part.Close()
		if err != nil {
			return fmt.Errorf("read multipart field %s: %w", name, err)
		}

		fileName := strings.TrimSpace(part.FileName())
		// File parts carry edit references or a nonempty mask. Preserve their
		// decoded bytes with filename and content type metadata. Text parts use
		// scalar parsing after the file-part path has been handled.
		if fileName != "" {
			partContentType := strings.TrimSpace(part.Header.Get("Content-Type"))
			if name == "mask" && len(data) > 0 {
				req.HasMask = true
				width, height := ParseOpenAIImageDimensions(part.Header)
				maskUpload := ImageUpload{
					FieldName:   name,
					FileName:    fileName,
					ContentType: partContentType,
					Data:        data,
					Width:       width,
					Height:      height,
				}
				req.MaskUpload = &maskUpload
			}
			if name == "image" || strings.HasPrefix(name, "image[") {
				width, height := ParseOpenAIImageDimensions(part.Header)
				req.Uploads = append(req.Uploads, ImageUpload{
					FieldName:   name,
					FileName:    fileName,
					ContentType: partContentType,
					Data:        data,
					Width:       width,
					Height:      height,
				})
			}
			continue
		}

		value := strings.TrimSpace(string(data))
		if imageGIFControl(name) {
			controls[name]++
		}
		gifSeen = gifSeen || name == "output_format" && strings.EqualFold(value, "gif") || name == "frame_count" || name == "frame_duration_ms"
		switch name {
		case "model":
			req.Model = value
			req.ExplicitModel = value != ""
		case "prompt":
			req.Prompt = value
		case "size":
			req.Size = value
			req.ExplicitSize = value != ""
		case "response_format":
			req.ResponseFormat = strings.ToLower(value)
		case "stream":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("invalid stream field value")
			}
			req.Stream = parsed
		case "n":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return fmt.Errorf("n must be a positive integer")
			}
			req.N = n
		case "quality":
			req.Quality = value
			req.HasNativeOptions = true
		case "background":
			req.Background = value
			req.HasNativeOptions = true
		case "output_format":
			req.OutputFormat = value
			req.HasNativeOptions = true
		case "moderation":
			req.Moderation = value
			req.HasNativeOptions = true
		case "input_fidelity":
			req.InputFidelity = value
			req.HasNativeOptions = true
		case "style":
			req.Style = value
			req.HasNativeOptions = true
		case "output_compression":
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid output_compression field value")
			}
			req.OutputCompression = &n
			req.HasNativeOptions = true
		case "frame_count", "frame_duration_ms":
			number, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
			if name == "frame_count" {
				req.FrameCount = &number
			} else {
				req.FrameDurationMS = &number
			}
			req.HasNativeOptions = true
		case "partial_images":
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid partial_images field value")
			}
			req.PartialImages = &n
			req.HasNativeOptions = true
		default:
			// A case-normalized native option can still require native execution
			// even when its exact spelling had no dedicated scalar assignment above.
			if IsOpenAINativeImageOption(name) && value != "" {
				req.HasNativeOptions = true
			}
		}
	}

	if gifSeen {
		for field, count := range controls {
			if count > 1 {
				return fmt.Errorf("duplicate GIF control: %s", field)
			}
		}
	}
	if len(req.Uploads) == 0 && req.IsEdits() {
		return fmt.Errorf("image file is required")
	}
	return nil
}

// ParseOpenAIImageDimensions returns zero width and height for the multipart
// upload parser. The header argument is unused; consumers needing dimensions
// inspect the retained image bytes through their own decoding path.
func ParseOpenAIImageDimensions(_ textproto.MIMEHeader) (int, int) {
	return 0, 0
}

// ApplyOpenAIImagesDefaults sets a nonpositive output count to one and a blank
// model to gpt-image-2. Nil requests are ignored; existing nonblank models are
// trimmed while explicit-selection flags retain parsed evidence.
func ApplyOpenAIImagesDefaults(req *ImageRequest) {
	if req == nil {
		return
	}
	if req.N <= 0 {
		req.N = 1
	}
	if strings.TrimSpace(req.Model) != "" {
		req.Model = strings.TrimSpace(req.Model)
		return
	}
	req.Model = "gpt-image-2"
}

// NormalizeOpenAIImagesEndpointPath selects a supported endpoint by substring
// in trimmed, case-sensitive path text. Generations wins when both substrings
// occur; unrecognized paths return empty for the request parser to reject.
func NormalizeOpenAIImagesEndpointPath(path string) string {
	trimmed := strings.TrimSpace(path)
	switch {
	case strings.Contains(trimmed, "/images/generations"):
		return OpenAIImagesGenerationsEndpoint
	case strings.Contains(trimmed, "/images/edits"):
		return OpenAIImagesEditsEndpoint
	default:
		return ""
	}
}

// HasOpenAINativeImageOptions reports whether the supplied nonnil predicate
// finds any recognized native option path. It stops on the first match; the
// caller decides whether presence, null values, or nonblank content qualifies.
func HasOpenAINativeImageOptions(exists func(path string) bool) bool {
	return slices.ContainsFunc([]string{
		"background",
		"quality",
		"style",
		"output_format",
		"output_compression",
		"moderation",
		"input_fidelity",
		"partial_images",
		"frame_count",
		"frame_duration_ms",
	}, exists)
}

// IsOpenAINativeImageOption reports whether trimmed, case-insensitive text
// names a recognized native image option. Multipart parsing uses this for
// unhandled field names when deciding whether nonblank input requires native
// capability.
func IsOpenAINativeImageOption(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "background", "quality", "style", "output_format", "output_compression", "moderation", "input_fidelity", "partial_images", "frame_count", "frame_duration_ms":
		return true
	default:
		return false
	}
}

// ParseImageRequest collects image request evidence from JSON or recognized
// multipart input and applies count and model defaults. It returns a new
// request whose Body borrows the caller's bytes; keep that storage stable
// while the request is retained.
//
// Recognized multipart input uses the declared boundary, while every other
// media type uses complete JSON syntax validation before path extraction. A
// supported path substring selects generation or editing. Model-family
// acceptance, provider capability selection, and billing size classification
// remain with gateway consumers; successful parsing alone does not establish
// those decisions.
func ParseImageRequest(path, contentType string, body []byte) (*ImageRequest, error) {
	endpoint := NormalizeOpenAIImagesEndpointPath(path)
	if endpoint == "" {
		return nil, fmt.Errorf("unsupported images endpoint")
	}

	contentType = strings.TrimSpace(contentType)
	req := &ImageRequest{
		Endpoint:    endpoint,
		ContentType: contentType,
		N:           1,
		Body:        body,
	}
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		// Eight digest bytes provide sixteen hex characters for promptless affinity.
		req.BodyHash = hex.EncodeToString(sum[:8])
	}

	// Recognized multipart MIME selects file-part parsing. Other MIME values
	// and MIME parse errors follow JSON syntax validation before request path extraction.
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		req.Multipart = true
		if parseErr := ParseOpenAIImagesMultipartRequest(body, contentType, req); parseErr != nil {
			return nil, parseErr
		}
	} else {
		if len(body) == 0 {
			return nil, fmt.Errorf("request body is empty")
		}
		if !gjson.ValidBytes(body) {
			return nil, fmt.Errorf("failed to parse request body")
		}
		if parseErr := ParseOpenAIImagesJSONRequest(body, req); parseErr != nil {
			return nil, parseErr
		}
	}

	ApplyOpenAIImagesDefaults(req)
	if err := req.NormalizeGIF(); err != nil {
		return nil, err
	}

	return req, nil
}

// String returns a fixed label for formatting that uses the Stringer
// interface. That path omits prompts, upload bytes, and signed image URLs;
// other formatting modes follow their own representation rules.
func (*ImageRequest) String() string { return "image wire request" }

// NormalizeGIF validates animation-only controls and supplies normalized defaults.
// Images requests cannot stream GIFs or combine them with masks, previews, or
// compression. Static image requests reject supplied animation values instead of dropping them.
func (r *ImageRequest) NormalizeGIF() error {
	frames, duration, err := protocol.NormalizeGIFOptions(r.OutputFormat, r.FrameCount, r.FrameDurationMS)
	if err != nil {
		return err
	}
	if frames == 0 {
		return nil
	}
	if r.Stream || r.HasMask || r.PartialImages != nil || r.OutputCompression != nil {
		return fmt.Errorf("GIF output does not support streaming, partial_images, masks, or output_compression")
	}
	if r.Size != "" && r.Size != "auto" {
		widthText, heightText, found := strings.Cut(r.Size, "x")
		width, widthErr := strconv.Atoi(widthText)
		height, heightErr := strconv.Atoi(heightText)
		if !found || widthErr != nil || heightErr != nil || width <= 0 || height <= 0 || width%16 != 0 || height%16 != 0 {
			return fmt.Errorf("GIF size must be auto or positive dimensions divisible by 16")
		}
	}
	r.OutputFormat = "gif"
	r.FrameCount, r.FrameDurationMS = &frames, &duration
	return nil
}

// ValidateGIFJSONControls rejects duplicate controls on animated image requests.
// Native image fields and each Responses image tool are examined independently.
// A duplicated tools list is rejected when any occurrence requests GIF output,
// preventing provider eligibility and upstream parsing from choosing different values.
func ValidateGIFJSONControls(body []byte) error {
	_, err := validateGIFJSONObject(gjson.ParseBytes(body))
	return err
}

// validateGIFJSONObject checks one image request object and hosted image tools.
// It reports whether any checked occurrence requests animation, including an
// overwritten tools member. Non-animation requests retain their existing duplicate-key behavior.
func validateGIFJSONObject(value gjson.Result) (bool, error) {
	animated := false
	counts := map[string]int{}
	var failure error
	value.ForEach(func(key, field gjson.Result) bool {
		if imageGIFControl(key.String()) {
			counts[key.String()]++
		}
		if key.String() == "output_format" && strings.EqualFold(field.String(), "gif") || key.String() == "frame_count" || key.String() == "frame_duration_ms" {
			animated = true
		}
		if key.String() == "tools" {
			for _, tool := range field.Array() {
				child, err := validateGIFJSONObject(tool)
				animated = animated || child
				if err != nil {
					failure = err
					return false
				}
			}
		}
		return true
	})
	if failure != nil {
		return animated, failure
	}
	if animated {
		for field, count := range counts {
			if count > 1 {
				return true, fmt.Errorf("duplicate GIF control: %s", field)
			}
		}
	}
	return animated, nil
}

// imageGIFControl identifies fields whose ambiguity can alter animation eligibility.
func imageGIFControl(name string) bool {
	switch name {
	case "output_format", "frame_count", "frame_duration_ms", "stream", "partial_images", "output_compression", "mask", "input_image_mask", "tools", "type", "model", "action", "n":
		return true
	default:
		return false
	}
}
