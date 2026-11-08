package media

import (
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images.go parses image requests, selects required provider capabilities and
// resolves completion image counts.
// Image routing can validate a group-mapped model while retaining the
// client's requested model and request bytes. Parsing keeps native upload
// and option references, then classifies whether an implicit single-image
// request fits the basic capability or requires the full image endpoint.
//
// Capability selection combines explicit model/size evidence with
// streaming, count, mask, editing encoding and native options; provider
// model resolution validates both routed and upstream names. Completion
// count selection separately combines transport format, observed images and
// failure state, retaining partial output on streaming failures and using
// requested counts only on eligible successful paths. Conversion between
// gateway and upstream request structs copies scalar values while sharing
// referenced storage.

const (
	// ImageCapabilityBasic and ImageCapabilityNative identify the selection
	// requirement checked against a provider's supported OpenAI image
	// execution paths.
	ImageCapabilityBasic  ImageCapability = "images-basic"  // implicit single-image basic request
	ImageCapabilityNative ImageCapability = "images-native" // full native image endpoint request
)

// ImageCapability aliases the provider selection label for supported image
// execution paths.
type ImageCapability = providercore.OpenAIImagesCapability

// ImageUpload aliases native multipart image metadata and its retained byte
// storage.
type ImageUpload = upstreamcore.ImageUpload

// ImageRequest carries parsed image options and routing capability with
// shared input storage.
type ImageRequest struct {
	Endpoint    string // normalized native image endpoint path
	ContentType string // retained request payload MIME
	Multipart   bool   // multipart form input indicator
	Model       string // client model or parsed default
	// ExplicitModel records input presence before defaults.
	// ClassifyImageCapability requires native execution when this evidence is
	// true, even for a recognized default-family image model.
	ExplicitModel bool   // client supplied a nonblank model
	Prompt        string // trimmed image generation/edit prompt
	Stream        bool   // requested image streaming mode
	N             int    // requested image output count
	Size          string // requested image size spelling
	// ExplicitSize distinguishes supplied size from parser defaults.
	// Capability classification requires native execution for explicit size
	// regardless of the normalized billing tier.
	ExplicitSize   bool   // client supplied nonblank image size
	SizeTier       string // normalized image billing size tier
	ResponseFormat string // requested image response encoding
	Quality        string // requested image quality setting
	Background     string // requested image background setting
	OutputFormat   string // requested output image format
	Moderation     string // requested image moderation setting
	InputFidelity  string // requested input image fidelity
	Style          string // requested image style option
	// OutputCompression retains the native parser's optional integer pointer.
	// NativeImageRequest and ApplyNativeImageRequest share that pointer, so
	// consumers coordinate mutation or copy the pointed value.
	FrameCount        *int // requested animation frame count
	FrameDurationMS   *int // milliseconds per animation frame
	OutputCompression *int // optional image compression percentage
	// PartialImages retains the native parser's optional integer pointer.
	// Converted request structs share this value, so callers copy it before
	// independent mutation.
	PartialImages    *int // optional partial image event count
	HasMask          bool // input includes image mask
	HasNativeOptions bool // input includes native-only image options
	// RequiredCapability is computed after parse and can be updated by
	// ValidateRoutingModel. Selection uses the routed-model classification
	// while Model retains the client spelling.
	RequiredCapability ImageCapability // selected provider image capability
	// InputImageURLs retains the native parser's slice and is shared by
	// request conversion. Consumers keep its elements stable while moderation
	// or forwarding reads them.
	InputImageURLs []string // shared input image URL slice
	MaskImageURL   string   // input mask image URL
	// Uploads shares the native upload slice and each upload's Data bytes
	// across request conversion. Callers keep both the slice elements and
	// their byte storage stable while readers use them.
	Uploads []ImageUpload // shared parsed multipart image uploads
	// MaskUpload retains the native upload pointer and its Data storage.
	// Converted requests share both, so consumers copy them before independent
	// mutation.
	MaskUpload *ImageUpload // optional shared multipart mask upload
	// Body retains the caller's payload bytes through native parsing and
	// conversion. Callers keep this storage stable while retries or any
	// retained request consumer reads it.
	Body     []byte // shared original image request bytes
	BodyHash string `json:"-"` // hex-encoded truncated request SHA-256
}

// ParseImageRequest delegates JSON or multipart parsing, applies billing
// size and capability classification, and optionally validates the parsed
// model. Disabling model validation permits aliases for later routing
// validation; returned Body and native option/upload references remain
// shared with their source.
func ParseImageRequest(endpoint, contentType string, body []byte, validateModel bool) (*ImageRequest, error) {
	value, err := upstreamcore.ParseImageRequest(endpoint, contentType, body)
	if err != nil {
		return nil, err
	}
	req := &ImageRequest{}
	ApplyNativeImageRequest(req, value)
	req.SizeTier = NormalizeImageSizeTier(req.Size)
	req.RequiredCapability = ClassifyImageCapability(req)
	if validateModel {
		if err := req.ValidateRoutingModel(req.Model); err != nil {
			return nil, err
		}
	}
	return req, nil
}

// ImageExecutionPath selects OAuth image handling for exact oauth or
// setup-token provider types, and API-key handling for exact apikey. Other
// spellings return an unsupported-provider error without trimming or case
// folding.
func ImageExecutionPath(providerType string) (bool, error) {
	switch providerType {
	case "apikey":
		return false, nil
	case "oauth", "setup-token":
		return true, nil
	default:
		return false, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}

// ModerationBody delegates prompt and image extraction to the native
// request representation. Nil requests or requests with no usable
// moderation content return nil; any returned JSON bytes are separately
// allocated by native marshaling.
func (r *ImageRequest) ModerationBody() []byte {
	return NativeImageRequest(r).ModerationBody()
}

// IsEdits reports whether the native normalized endpoint is the editing
// endpoint. A nil request returns false; callers use this operation flag
// alongside Multipart when deciding required image capability.
func (r *ImageRequest) IsEdits() bool { return NativeImageRequest(r).IsEdits() }

// StickySessionSeed delegates deterministic request-seed construction to
// the native representation. Nil returns empty; native construction
// combines endpoint/model/size/prompt and adds BodyHash when prompt is
// blank, so callers retain the parsed request values.
func (r *ImageRequest) StickySessionSeed() string {
	return NativeImageRequest(r).StickySessionSeed()
}

// ValidateRoutingModel validates routingModel and updates the selection
// capability while keeping the client model in r.Model. Invalid model
// returns before mutation; a nil receiver with a valid model returns nil.
//
// A shallow value copy receives the trimmed routing model for
// classification, while the original request keeps its body and model
// identity. Only RequiredCapability is installed back into r. This lets
// group mapping affect provider selection without rewriting the
// client-facing request model or copying uploads and option storage.
func (r *ImageRequest) ValidateRoutingModel(routingModel string) error {
	if err := ValidateImageModel(routingModel); err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	// Classify a value copy with the routed model so capability changes follow
	// group mapping while the client model and referenced request storage
	// retain their identities.
	routed := *r
	routed.Model = strings.TrimSpace(routingModel)
	r.RequiredCapability = ClassifyImageCapability(&routed)
	return nil
}

// ValidateImageModel requires a nonempty execution identity before mapping.
// Image capability belongs to the final selected route and is validated after
// provider mapping, allowing configured aliases to reach their declared target.
func ValidateImageModel(model string) error {
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("images endpoint requires a model")
	}
	return nil
}

// ClassifyImageCapability selects basic capability only for an implicit
// GPT-image single-output request using supported response encoding. Nil
// input, explicit model/size, streaming, multiple
// outputs, masks or native options require the native capability.
//
// Editing also requires native capability unless input is multipart, and a
// nonempty response format other than b64_json requires native handling.
// These observations combine before the basic result is returned, so a
// parser default does not erase evidence that the client explicitly chose a
// model or size.
func ClassifyImageCapability(req *ImageRequest) ImageCapability {
	if req == nil {
		return ImageCapabilityNative
	}
	// Explicit model or size selection requires the native endpoint even when
	// its value resembles a parser default, retaining the supplied-option
	// distinction during routing.
	if req.ExplicitModel || req.ExplicitSize {
		return ImageCapabilityNative
	}
	// Streaming, output count, masks and native options jointly rule out the
	// implicit basic path; any one observation requires the provider's native
	// image capability.
	if req.Stream || req.N != 1 || req.HasMask || req.HasNativeOptions {
		return ImageCapabilityNative
	}
	// Editing JSON bodies require native handling because only multipart
	// editing can reach the basic image compatibility path selected by this
	// classifier.
	if req.IsEdits() && !req.Multipart {
		return ImageCapabilityNative
	}
	// The basic path accepts absent or base64 JSON format; other requested
	// encodings require native handling so response conversion follows that
	// request choice.
	if req.ResponseFormat != "" && req.ResponseFormat != "b64_json" {
		return ImageCapabilityNative
	}
	return ImageCapabilityBasic
}

// NormalizeImageSizeTier delegates dimension/tier recognition to billing
// pricing. Empty, auto or unrecognized size uses the shared 2K billing
// default; callers retain the input spelling separately for evidence.
func NormalizeImageSizeTier(size string) string {
	return pricing.NormalizeImageBillingTierOrDefault(size)
}

// NativeImageRequest copies gateway request values into a new upstream
// struct, or returns nil for nil input. Body, upload/URL slices, mask
// upload and integer option pointers remain shared; callers copy referenced
// storage before independent mutation.
func NativeImageRequest(value *ImageRequest) *upstreamcore.ImageRequest {
	if value == nil {
		return nil
	}
	return &upstreamcore.ImageRequest{
		Endpoint:          value.Endpoint,
		ContentType:       value.ContentType,
		Multipart:         value.Multipart,
		Model:             value.Model,
		ExplicitModel:     value.ExplicitModel,
		Prompt:            value.Prompt,
		Stream:            value.Stream,
		N:                 value.N,
		Size:              value.Size,
		ExplicitSize:      value.ExplicitSize,
		SizeTier:          value.SizeTier,
		ResponseFormat:    value.ResponseFormat,
		Quality:           value.Quality,
		Background:        value.Background,
		OutputFormat:      value.OutputFormat,
		Moderation:        value.Moderation,
		InputFidelity:     value.InputFidelity,
		Style:             value.Style,
		FrameCount:        value.FrameCount,
		FrameDurationMS:   value.FrameDurationMS,
		OutputCompression: value.OutputCompression,
		PartialImages:     value.PartialImages,
		HasMask:           value.HasMask,
		HasNativeOptions:  value.HasNativeOptions,
		InputImageURLs:    value.InputImageURLs,
		MaskImageURL:      value.MaskImageURL,
		Uploads:           value.Uploads,
		MaskUpload:        value.MaskUpload,
		Body:              value.Body,
		BodyHash:          value.BodyHash,
	}
}

// ApplyNativeImageRequest installs native values into an existing gateway
// request when both pointers are nonnil. It shares byte/slice/option
// references and leaves RequiredCapability for gateway classification; nil
// inputs leave the target unchanged.
func ApplyNativeImageRequest(target *ImageRequest, value *upstreamcore.ImageRequest) {
	if target == nil || value == nil {
		return
	}
	target.Endpoint = value.Endpoint
	target.ContentType = value.ContentType
	target.Multipart = value.Multipart
	target.Model = value.Model
	target.ExplicitModel = value.ExplicitModel
	target.Prompt = value.Prompt
	target.Stream = value.Stream
	target.N = value.N
	target.Size = value.Size
	target.ExplicitSize = value.ExplicitSize
	target.SizeTier = value.SizeTier
	target.ResponseFormat = value.ResponseFormat
	target.Quality = value.Quality
	target.Background = value.Background
	target.OutputFormat = value.OutputFormat
	target.Moderation = value.Moderation
	target.InputFidelity = value.InputFidelity
	target.Style = value.Style
	target.FrameCount = value.FrameCount
	target.FrameDurationMS = value.FrameDurationMS
	target.OutputCompression = value.OutputCompression
	target.PartialImages = value.PartialImages
	target.HasMask = value.HasMask
	target.HasNativeOptions = value.HasNativeOptions
	target.InputImageURLs = value.InputImageURLs
	target.MaskImageURL = value.MaskImageURL
	target.Uploads = value.Uploads
	target.MaskUpload = value.MaskUpload
	target.Body = value.Body
	target.BodyHash = value.BodyHash
}

// ResolveImageModels returns validated routed and upstream image models
// from request and mapping evidence. A nonblank trimmed groupMapped value
// takes precedence over the trimmed request; absent choice uses fallback,
// which is validated before the required resolve callback runs.
//
// The callback receives the selected routed spelling, and its returned
// upstream name is validated separately before both values are returned.
// This preserves the two identities for selection and provider execution
// rather than collapsing provider mapping into the client-facing model. Any
// failed validation returns an error and no usable model pair.
func ResolveImageModels(requested, groupMapped, fallback string, resolve func(string) string) (string, string, error) {
	model := strings.TrimSpace(requested)
	if mapped := strings.TrimSpace(groupMapped); mapped != "" {
		model = mapped
	}
	// When request and mapping supply no usable name, validate the fallback
	// before invoking provider resolution so that callback receives an
	// nonempty execution identity.
	if model == "" {
		model = fallback
	}
	if err := ValidateImageModel(model); err != nil {
		return "", "", err
	}
	upstream := resolve(model)
	if err := ValidateImageModel(upstream); err != nil {
		return "", "", err
	}
	return model, upstream, nil
}

// ImageOutcome selects retained image count from observed output, requested
// count, transport format and failure state. API-key failures retain positive
// completed-image observations in either client mode; OAuth failures retain
// them only for streaming clients under the Responses conversion policy.
//
// Successful OAuth, nonstream or non-SSE handling can use requested count
// when observation is nonpositive. Successful API-key SSE keeps its
// observed count, including zero, and still returns true. Callers therefore
// distinguish retention from a positive count before deciding whether image
// completion observations are available.
func ImageOutcome(stream, oauth, eventStream bool, requested, observed int, failure error) (int, bool) {
	// Native streams can feed buffered clients, and URL conversion can fail
	// after generation. Retain their observed completions while keeping OAuth's
	// existing buffered failure policy and never substituting requested counts.
	if failure != nil && (observed <= 0 || (oauth && !stream)) {
		return 0, false
	}
	count := observed
	// Use requested count only for successful eligible transports with no
	// positive observation, while API-key SSE must keep the image count
	// actually observed.
	if failure == nil && count <= 0 && (oauth || !stream || !eventStream) {
		count = requested
	}
	return count, true
}
