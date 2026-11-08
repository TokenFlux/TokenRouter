package creative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/image/webp"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// public.go lists available creative models and validates, prices, and
// provisions personal image tasks.
// Personal image submission checks current group access and one concrete
// configured model before creating a task. User discovery reads administrator
// allowlist entries directly, while administrator candidate discovery combines
// finite configured names across providers and groups. Image capabilities
// choose parameter options independently from pricing, whose quote
// availability is checked before new task funding. Output formats are exposed
// per admitted operation so a generation route cannot grant GIF editing support.
//
// Creation validates and moderates before idempotency lookup, so replay still
// depends on current admission. A new task obtains a price snapshot and hidden
// Key, then persists metadata before reservation, transient input writes, and
// enqueue progress. Those cross-store steps keep separate recovery markers;
// creation failures can leave earlier work present for outbox recovery. Prompt
// and media hashes identify persisted requests while their actual content
// remains in temporary storage.

// Platform names select image routes and adapters; MIME and input-size limits
// govern task defaults, PNG masks, and encoded Gemini request admission.
const (
	PlatformOpenAI = "openai"
	PlatformGemini = "gemini"
	PlatformGrok   = "grok"

	DefaultCreativeResponseMime = "image/png"

	MaxCreativeMaskBytes         = 4 << 20
	MaxCreativeGeminiInlineBytes = 20 << 20
)

// ErrCreativeContentBlocked identifies a returned content denial that blocks
// submission before hidden Key creation and task funds reservation are attempted.
var ErrCreativeContentBlocked = infraerrors.New(infraerrors.CategoryForbidden, "CREATIVE_CONTENT_BLOCKED", "creative content failed moderation")

type (
	// UserAccess supplies current user group-binding permission for
	// creative model and task admission.
	UserAccess interface {
		// CanBindGroup checks whether the authenticated user can bind
		// the supplied
		// group. The exclusive flag comes from the current group record, and
		// admission checks this before any task is funded.
		CanBindGroup(int64, bool) bool
	}
	// UserReader supplies authenticated user access data for creative group
	// permission checks.
	UserReader interface {
		// GetByID returns binding access for the supplied user identifier. Public
		// converts a read failure or absent value to its configured UserNotFound
		// error before evaluating the selected creative group.
		GetByID(context.Context, int64) (UserAccess, error)
	}
)

// GroupView carries current creative admission flags, group display data, and
// image model/protocol rules.
type GroupView struct {
	ID   int64  // selected group row identifier
	Name string // current group display name
	// ClaudeCodeOnly excludes the group from Creative Studio; no fallback
	// group is selected.
	// client restricted group flag
	ClaudeCodeOnly bool
	// IsExclusive supplies the user binding check, while Active and
	// AllowImageGeneration gate creative admission. Discovery and submission
	// require these current group flags before a model can create funded work.
	// IsExclusive is exclusive group binding flag; AllowImageGeneration is group
	// image admission enabled flag; Active is current group active flag
	IsExclusive, AllowImageGeneration, Active bool
	RateMultiplier                            float64 // group default price multiplier
	// Operations and ProtocolFallbacks retain the supplied route maps;
	// RoutingPolicy is cloned by each query. Callers keep the supplied maps
	// stable while model and operation eligibility is resolved.
	// admitted operations by platform
	Operations map[string][]string
	// group protocol conversion route rules
	ProtocolFallbacks map[protocol.ProtocolID][]protocol.ProtocolID
	// current group model routing policy
	RoutingPolicy routing.GroupRoutingPolicy
	// ModelsList contains enabled custom request names included in finite
	// administrator candidates. Queries keep this supplied slice stable while
	// combining it with provider and policy names.
	ModelsList []string // enabled custom public request names
}

// GroupReader supplies current group metadata for creative submission and
// active model discovery.
type GroupReader interface {
	// GetByIDLite reads current creative flags and model policy for one exact
	// group. Submission rejects a missing or inactive group before managed Key
	// creation and funds reservation.
	GetByIDLite(context.Context, int64) (*GroupView, error)
	// ListActive reads active groups for user discovery and administrator
	// candidates. Each caller applies its own binding and image-route checks
	// before those groups can contribute returned model entries.
	ListActive(context.Context) ([]GroupView, error)
}

// CatalogProvider supplies a provider scheduling state, platform, model
// mappings, and protocol capabilities.
type CatalogProvider interface {
	// SupportsImageGIF reports the provider's animation switch and endpoint mode.
	// Model format support is resolved separately; this method cannot grant a
	// model capability missing from its final-route attributes.
	SupportsImageGIF(protocol.ProtocolID) bool

	// PlatformID returns the provider platform used to select creative image
	// adapter rules.
	PlatformID() string
	// AllowsProtocol checks a creative operation protocol against provider
	// capabilities and group fallback routes. Model resolution applies this check
	// before adding that provider operation to the advertised route.
	AllowsProtocol(protocol.ProtocolID, map[protocol.ProtocolID][]protocol.ProtocolID) bool
	// IsSchedulable reports whether the provider current scheduling state permits
	// creative route discovery.
	IsSchedulable() bool
	// GetModelMapping returns configured provider mapping names used for finite
	// candidate generation. Actual admission still resolves each candidate and
	// checks provider support, protocols, group restrictions, and image output.
	GetModelMapping() map[string]string
	// GetConfiguredRequestModels returns concrete provider candidate names from
	// prepared execution rules. A candidate contributes discovery input, while
	// model resolution separately verifies scheduling, support, protocol routing,
	// and image capability.
	GetConfiguredRequestModels() []string
	// ResolveMappedModel applies one provider model mapping and reports whether
	// it matched. Creative route construction retains nonblank mapped spelling
	// and uses the requested name when that spelling is blank.
	ResolveMappedModel(string) (string, bool)
	// IsModelSupported reports provider admission for a group-mapped request
	// name. A rejected provider contributes no operation or parameter
	// observations to the resolved creative model route for that name.
	IsModelSupported(string) bool
}

// ProviderReader reads eligible group/platform candidates for creative model
// and image capability resolution.
type ProviderReader interface {
	// ListSchedulableByGroupIDAndPlatform returns candidates for one selected
	// group and platform. A creative query reads each enabled platform list once
	// and reuses it across concrete model resolution.
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]CatalogProvider, error)
}

// UserRateReader supplies optional user-specific balance rates used by
// creative task price snapshots.
type UserRateReader interface {
	// GetByUserAndGroup returns an optional balance multiplier for this user and
	// group. ResolveCreativePricing retains the group default when a read fails
	// or the returned override is absent.
	GetByUserAndGroup(context.Context, int64, int64) (*float64, error)
}

// SettingReader supplies current creative enablement and exact administrator
// model settings for public admission.
type SettingReader interface {
	// IsCreativeEnabled reports the runtime switch combined with process settings
	// by Public.Enabled. False blocks new creation and makes user model discovery
	// return an empty successful directory result.
	IsCreativeEnabled(context.Context) bool
	// GetCreativeModelSettings returns exact administrator group/model operations
	// for public admission. Empty settings close user discovery and submission,
	// while workers independently check their saved task against current group
	// model policy.
	GetCreativeModelSettings(context.Context) []CreativeModelSetting
}

// ModerationInput pairs request identity and scope with the temporary review
// request body.
type ModerationInput struct {
	RequestID string // local request ID value
	// UserID is authenticated request owner identifier; BillingUserID is request
	// billing owner identifier
	UserID, BillingUserID int64
	// GroupID points into the validated group view during review. Callers keep
	// that identifier stable while a Check implementation consumes the input.
	// target request group identifier
	GroupID *int64
	// GroupName is target group display name; Endpoint is creative request
	// endpoint label; Platform is selected image route platform; Model is public
	// request model name; Protocol is review request protocol label
	GroupName, Endpoint, Platform, Model, Protocol string
	// Body holds freshly encoded prompt and media data for Check. The review
	// consumer treats these bytes as immutable while evaluating or retaining them.
	// temporary JSON review request bytes
	Body []byte
	// review input retention exclusion flag
	// NoMediaRetention requests review logging without media snapshots or body
	// excerpts. ModerateCreativeRequest sets it true before delegating to the
	// configured moderation implementation.
	NoMediaRetention bool
}
type (
	// ModerationDecision carries the review permission returned for one
	// temporary creative request.
	ModerationDecision struct {
		Allowed bool // review permitted request flag
	}
	// Moderator evaluates a temporary creative prompt and media body before
	// task creation.
	Moderator interface {
		// Check reviews a temporary creative request and returns an optional
		// permission decision. Public requests NoMediaRetention, allows service
		// errors, and blocks a successful nonnil denial before creating a task.
		Check(context.Context, ModerationInput) (*ModerationDecision, error)
	}
)

// PublicOptions supplies process feature gating, upload and prompt limits, and
// the default image tier.
type PublicOptions struct {
	Enabled        bool // process creative enable flag
	MaxPromptChars int  // Unicode code points
	// MaxAssetBytes is bytes; MaxTotalInputBytes is bytes
	MaxAssetBytes, MaxTotalInputBytes int64
	DefaultImageSize                  string // default requested image size tier
}

// Public resolves creative admission and prices, then provisions persistent
// task metadata and transient inputs.
type Public struct {
	// Repo, UserRepo, GroupRepo, ProviderRepo, Queue, TransientStore, Results,
	// Settings, EnsureKey, and ImageUnitPrice are configured before serving. Keep
	// these shared dependencies and optional callbacks stable while concurrent
	// requests use them.
	// shared attribute query service
	Attributes *routing.ModelAttributeService
	// Now supplies public query time for expiry and receipt checks. A nil value
	// lets Queries use system time; Results owns its separate recovery clock.
	Now func() time.Time // optional public query clock
	// task metadata persistence repository
	Repo      CreativeRunRepository
	UserRepo  UserReader  // current user access reader
	GroupRepo GroupReader // current creative group rules reader
	// group platform candidate provider reader
	ProviderRepo ProviderReader
	// UserGroupRateRepo optionally overrides the balance multiplier for creation.
	// Missing readers and failed reads retain the current group default rate.
	UserGroupRateRepo UserRateReader
	Queue             CreativeRunQueue // creative task admission queue
	// temporary execution input storage
	TransientStore CreativeTransientStore
	Results        *Results // task funds and result service
	// runtime creative admission settings reader
	Settings SettingReader
	Options  PublicOptions // process feature and input settings
	// UserNotFound classifies every failed or absent UserRepo lookup in
	// ListModels and ValidateCreateParams, including read failures that did not
	// establish confirmed user absence.
	UserNotFound error // user absence error classification
	// EnsureKey supplies the hidden execution Key identifier before metadata
	// creation. Configure this callback before CreateRun; its failures stop
	// reservation and input provisioning.
	EnsureKey func(context.Context, int64, int64) (int64, error)
	// GroupMapping optionally supplies linked pricing model selection. A nil
	// callback leaves the resolved upstream name as the quote identity.
	GroupMapping func(context.Context, int64, string) routing.GroupMappingResult
	// ImageUnitPrice returns a unit amount and independent quote availability.
	// Configure it before discovery or creation so a valid zero quote remains
	// distinct from missing pricing.
	ImageUnitPrice func(context.Context, *GroupView, string, string) (float64, bool)
	// SubscriptionMultiplier optionally supplies the effective reservation rate.
	// A nil callback or false result keeps the selected balance rate as the
	// estimate multiplier while both source rates remain saved.
	SubscriptionMultiplier func(context.Context, int64, *GroupView, float64) (float64, bool)
	// Moderation optionally reviews validated temporary content before task creation.
	// A nil service permits creation; service errors are observed and allowed.
	Moderation Moderator
	// RequestID optionally supplies the current request identity for moderation.
	// A nil callback or blank result leaves the newly generated review ID in use.
	RequestID func(context.Context) string
	// Observe receives creative admission and provisioning diagnostics synchronously.
	// A nil callback skips those diagnostics; configured observers support
	// concurrent requests sharing this Public instance.
	Observe func(string, ...any)
}

// CreativeModelCapabilities exposes parameter choices and counts resolved for
// a creative image model route.
type CreativeModelCapabilities struct {
	AspectRatios  []string // supported image shape options
	Qualities     []string // supported image quality options
	OutputFormats []string // exposed output format options
	// optional compression range metadata
	// OutputCompression remains nil in CreativeCapabilitiesForModel. The
	// multipart parser rejects client compression fields even though this
	// capability representation retains a place for range metadata.
	OutputCompression *CreativeNumericRange
	BackgroundOptions []string // supported image background options
	ThinkingLevels    []string // supported image reasoning options
	MaxOutputCount    int      // maximum admitted output count
	// MaxReferenceImages constrains submitted sources only when positive in
	// ValidateCreateParams. A supplied zero limit bypasses that reference-count
	// comparison rather than rejecting every source.
	MaxReferenceImages int // maximum admitted source count
}

// creativeModelRoute combines a selected platform/model with common provider
// and operation-specific capability attributes.
type creativeModelRoute struct {
	// Attributes combines observations across eligible providers for the chosen
	// platform. OperationAttributes keeps separate operation intersections, and
	// submission uses the selected operation attributes when that entry is
	// available.
	// common model capability attributes
	Attributes modelcatalog.Attributes
	// common attributes per operation
	OperationAttributes map[string]modelcatalog.Attributes
	// Platform names the chosen platform; Model is its first resolved
	// upstream image name.
	Platform, Model string
	Operations      []string // admitted operations by platform
}

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

// CreativePricingSnapshot records image base price, source multipliers, and
// estimated amount at task creation.
type CreativePricingSnapshot struct {
	// BaseUnitPrice, SubscriptionRateMultiplier, BalanceRateMultiplier, and
	// EstimatedCost retain the quote selected at creation. An effective
	// subscription quote can change the estimate while later funds commands
	// retain both separate source rates.
	BaseUnitPrice              float64 // USD per image
	SubscriptionRateMultiplier float64 // saved subscription price multiplier
	BalanceRateMultiplier      float64 // saved balance price multiplier
	PlanGroupRateEnabled       bool    // plan group multiplier enabled flag
	EstimatedCost              float64 // USD
}

// warn sends a creative request event and attributes to Observe synchronously.
// A nil callback drops the diagnostic; supplied values pass through unchanged
// for admission or provisioning observations.
func (s *Public) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// Enabled combines process and runtime switches with task and group repository
// availability. Nil Public or absent Settings returns false; this gate does
// not validate every dependency later used by creation.
func (s *Public) Enabled(ctx context.Context) bool {
	return s != nil && s.Repo != nil && s.GroupRepo != nil && s.Options.Enabled && s.Settings != nil && s.Settings.IsCreativeEnabled(ctx)
}

// MaxPromptChars returns a positive configured Unicode-code-point limit or
// eight thousand. Nil Public and nonpositive configuration use the same
// default for capability output and normalized prompt validation.
func (s *Public) MaxPromptChars() int {
	if s != nil && s.Options.MaxPromptChars > 0 {
		return s.Options.MaxPromptChars
	}
	return 8000
}

// MaxAssetBytes returns a positive configured per-file byte limit or
// thirty-two MiB. Nil Public and nonpositive configuration use that default
// for HTTP upload parsing and domain image validation.
func (s *Public) MaxAssetBytes() int64 {
	if s != nil && s.Options.MaxAssetBytes > 0 {
		return s.Options.MaxAssetBytes
	}
	return 33554432
}

// MaxTotalInputBytes returns a positive configured combined media byte limit
// or sixty-four MiB. Nil Public and nonpositive configuration use that default
// for HTTP parsing and creation input validation.
func (s *Public) MaxTotalInputBytes() int64 {
	if s != nil && s.Options.MaxTotalInputBytes > 0 {
		return s.Options.MaxTotalInputBytes
	}
	return 67108864
}

// DefaultImageSize returns trimmed nonblank configured size or the one-K
// default. The chosen route still validates this default before submission can
// create or fund a task.
func (s *Public) DefaultImageSize() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Options.DefaultImageSize)
	}
	return "1K"
}

// mappedCatalogModel returns trimmed nonblank provider mapping or the supplied
// model as fallback. A nil provider returns empty; the returned mapping
// matched flag does not influence this fallback selection.
func mappedCatalogModel(a CatalogProvider, m string) string {
	if a == nil {
		return ""
	}
	value, _ := a.ResolveMappedModel(m)
	// A blank mapped value cannot identify an upstream request model. Use the
	// concrete supplied name so subsequent group restriction and image capability
	// checks still evaluate a model identity instead of empty text.
	if strings.TrimSpace(value) == "" {
		return m
	}
	return strings.TrimSpace(value)
}

// GetCapabilities returns server prompt, media, and MIME limits for the
// browser. It reads process-option defaults independently from feature
// enablement, allowing authenticated limit reads without a browser workspace
// scope.
func (s *Public) GetCapabilities(ctx context.Context) *CreativeCapabilitiesResponse {
	return &CreativeCapabilitiesResponse{
		MaxPromptChars:     s.MaxPromptChars(),
		MaxAssetBytes:      s.MaxAssetBytes(),
		MaxTotalInputBytes: s.MaxTotalInputBytes(),
		MaxMaskBytes:       MaxCreativeMaskBytes,
		AllowedMIMETypes:   []string{"image/png", "image/jpeg", "image/webp"},
	}
}

// ListModels returns configured priced image routes that the current user can
// bind. Disabled creative and an empty administrator allowlist return an empty
// successful directory.
//
// Each eligible group prepares providers once and directly resolves saved
// concrete names. Configured operations intersect the resolved route
// operations, while route capabilities determine sizes independently from
// price overrides. A missing first-tier quote excludes a model. Display prices
// use the group multiplier, whereas creation separately calculates its user
// and subscription funding estimate. Output format choices use each admitted
// operation's route attributes; their legacy union excludes disabled operations.
func (s *Public) ListModels(ctx context.Context, userID int64) (*CreativeModelsResponse, error) {
	if !s.Enabled(ctx) {
		// Disabled discovery uses an empty successful array for the
		// browser picker.
		// Task creation retains its separate gate, so the UI can show
		// disabled model
		// availability while submission receives the feature-disabled error.
		return &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}, nil
	}
	user, err := s.UserRepo.GetByID(ctx, userID)
	// User access failures share the configured not-found result here, including
	// store outages. Return that admission classification before reading groups
	// rather than exposing the underlying user-store error to this caller.
	if err != nil || user == nil {
		return nil, s.UserNotFound
	}
	settings := s.CreativeModelSettings(ctx)
	modelSettings := CreativeModelSettingsIndex(settings)
	if len(modelSettings) == 0 {
		return &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}, nil
	}
	groups, err := s.GroupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}
	for i := range groups {
		group := &groups[i]
		if !user.CanBindGroup(group.ID, group.IsExclusive) {
			continue
		}
		if !group.AllowImageGeneration || !group.Active || group.ClaudeCodeOnly {
			continue
		}
		platformOperations := groupOperations(group)
		if len(platformOperations) == 0 {
			continue
		}
		query, err := s.prepareCreativeModels(ctx, group)
		if err != nil {
			return nil, err
		}
		routes, err := query.resolveModels(ctx, creativeSettingsModels(settings, group.ID))
		if err != nil {
			return nil, err
		}
		models := creativeModelsFromRoutes(routes)
		if err != nil {
			return nil, err
		}
		modelNames := make([]string, 0, len(models))
		for model := range models {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		for _, model := range modelNames {
			operations, configured := CreativeOperationsForModel(modelSettings, group.ID, model, routes[model].Operations)
			if !configured || len(operations) == 0 {
				continue
			}
			// Read route tiers before quote availability. A missing
			// per-size override
			// cannot remove a supported capability tier here,
			// although the first-tier
			// quote check can later exclude an unpriced model from
			// this directory.
			finalModel := models[model]
			if finalModel == "" {
				finalModel = model
			}
			imageSizes := creativeRouteImageSizes(routes[model])
			if len(imageSizes) == 0 {
				continue
			}
			capabilities := CreativeCapabilitiesForModel(routes[model].Platform, finalModel, routes[model].Attributes)
			pricingModel := s.BillingModel(ctx, group, model, finalModel)
			if _, ok := s.ImageUnitPrice(ctx, group, pricingModel, imageSizes[0]); !ok {
				continue
			}
			// Format eligibility follows the same operation attributes as submission.
			// Restrict the legacy union too, so disabled generation cannot expose GIF
			// when the only admitted operation has no usable animation route.
			outputFormats := []string{}
			formatsByOperation := make(map[string][]string, len(operations))
			for _, operation := range operations {
				attrs := routes[model].Attributes
				if specific, ok := routes[model].OperationAttributes[operation]; ok {
					attrs = specific
				}
				formats := []string{"png"}
				if operation != CreativeOperationInpaint && attrs.Media != nil && slices.Contains(attrs.Media.OutputFormats, "gif") {
					formats = append(formats, "gif")
					outputFormats = []string{"png", "gif"}
				}
				formatsByOperation[operation] = formats
			}
			out.Data = append(out.Data, CreativeModelPublic{
				GroupID:                  group.ID,
				GroupName:                group.Name,
				Model:                    model,
				Operations:               operations,
				ImageSizes:               imageSizes,
				AspectRatios:             capabilities.AspectRatios,
				Qualities:                capabilities.Qualities,
				OutputFormats:            outputFormats,
				OutputFormatsByOperation: formatsByOperation,
				GIFFrameCount:            creativeGIFRange(outputFormats, false),
				GIFFrameDurationMS:       creativeGIFRange(outputFormats, true),
				OutputCompression:        capabilities.OutputCompression,
				BackgroundOptions:        capabilities.BackgroundOptions,
				ThinkingLevels:           capabilities.ThinkingLevels,
				MaxOutputCount:           capabilities.MaxOutputCount,
				MaxReferenceImages:       capabilities.MaxReferenceImages,
				Price512:                 s.CreativePrice(ctx, group, pricingModel, "512"),
				Price1K:                  s.CreativePrice(ctx, group, pricingModel, "1K"),
				Price2K:                  s.CreativePrice(ctx, group, pricingModel, "2K"),
				Price4K:                  s.CreativePrice(ctx, group, pricingModel, "4K"),
			})
		}
	}
	return out, nil
}

// ListCreativeModelCandidates returns finite active image routes for
// administrator configuration. It excludes client-restricted groups and groups
// without image operations, while applying no administrator user-binding
// filter.
//
// Candidates combine saved creative settings, group request names, and
// prepared provider rules. Admission and image-capability checks select each
// route and operation list, and names are sorted within the group. The
// administrator can configure exclusive groups through this view while
// ordinary user discovery still checks that user binding permission before
// listing them.
func (s *Public) ListCreativeModelCandidates(ctx context.Context) ([]CreativeModelCandidate, error) {
	if s == nil || s.GroupRepo == nil {
		return nil, errors.New("creative group repository is not configured")
	}
	groups, err := s.GroupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CreativeModelCandidate, 0)
	for i := range groups {
		group := &groups[i]
		if !group.Active || !group.AllowImageGeneration || group.ClaudeCodeOnly {
			continue
		}
		operations := groupOperations(group)
		if len(operations) == 0 {
			continue
		}
		routes, err := s.creativeModelRoutes(ctx, group)
		models := creativeModelsFromRoutes(routes)
		if err != nil {
			return nil, err
		}
		modelNames := make([]string, 0, len(models))
		for model := range models {
			if len(creativeRouteImageSizes(routes[model])) == 0 {
				continue
			}
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		for _, model := range modelNames {
			out = append(out, CreativeModelCandidate{
				GroupID:    group.ID,
				GroupName:  group.Name,
				Platform:   routes[model].Platform,
				Model:      model,
				Operations: append([]string(nil), routes[model].Operations...),
			})
		}
	}
	return out, nil
}

// CreativeModelSettings returns the exact runtime model allowlist used for
// public admission. Missing Public or Settings returns an empty list, and the
// configured reader determines parsing defaults and returned collection
// ownership.
func (s *Public) CreativeModelSettings(ctx context.Context) []CreativeModelSetting {
	if s == nil || s.Settings == nil {
		return []CreativeModelSetting{}
	}
	return s.Settings.GetCreativeModelSettings(ctx)
}

// CreativePrice returns the quoted image unit amount multiplied by the group
// display rate. Missing quotes return zero, while successful zero quotes
// remain valid through the separate availability result used during creation.
func (s *Public) CreativePrice(ctx context.Context, group *GroupView, model, imageSize string) float64 {
	unit, ok := s.ImageUnitPrice(ctx, group, model, imageSize)
	if !ok {
		// Display uses zero for a missing quote; creation separately
		// retains quote
		// availability so an unavailable amount cannot be admitted as a
		// free task.
		return 0
	}
	return unit * group.RateMultiplier
}

// CreativeOperationsForPlatform returns a new operation slice for a trimmed
// supported platform. OpenAI includes inpaint, Gemini and Grok allow
// generation/edit, and an unknown platform returns nil.
func CreativeOperationsForPlatform(platform string) []string {
	switch strings.TrimSpace(platform) {
	case PlatformOpenAI:
		return []string{CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint}
	case PlatformGemini, PlatformGrok:
		return []string{CreativeOperationGenerate, CreativeOperationEdit}
	default:
		return nil
	}
}

// CreativeCapabilitiesForModel returns media choices with one-output and
// one-reference defaults. Only the first supplied attribute value participates;
// its effort choices can populate ThinkingLevels even when Media is absent.
//
// Supplied attributes represent resolved route knowledge, so unknown media in
// that value retains empty media choices and the count defaults. Catalogue
// media is consulted only when attributes are omitted. Media choice slices
// are copied and present count pointers replace defaults, keeping explicit
// route observations separate from convenience lookups by platform and model.
func CreativeCapabilitiesForModel(platform, model string, attributes ...modelcatalog.Attributes) CreativeModelCapabilities {
	result := CreativeModelCapabilities{AspectRatios: []string{}, Qualities: []string{}, OutputFormats: []string{}, BackgroundOptions: []string{}, ThinkingLevels: []string{}, MaxOutputCount: 1, MaxReferenceImages: 1}
	var media *modelcatalog.MediaCapabilities
	// A supplied attribute result controls this conversion even when its media
	// is unknown. Catalogue media is consulted only when no attribute argument
	// was supplied, keeping explicit route resolution distinct from helper defaults.
	if len(attributes) > 0 {
		media = attributes[0].Media
		result.ThinkingLevels = attributes[0].Efforts()
	} else {
		media = modelcatalog.MediaForModel(platform, model)
	}
	if media == nil {
		return result
	}
	result.AspectRatios = append(result.AspectRatios, media.AspectRatios...)
	result.Qualities = append(result.Qualities, media.Qualities...)
	result.OutputFormats = append(result.OutputFormats, media.OutputFormats...)
	result.BackgroundOptions = append(result.BackgroundOptions, media.BackgroundOptions...)
	if media.MaxOutputCount != nil {
		result.MaxOutputCount = *media.MaxOutputCount
	}
	if media.MaxReferenceImages != nil {
		result.MaxReferenceImages = *media.MaxReferenceImages
	}
	return result
}

// creativeRouteImageSizes copies the resolved route's declared size choices.
// Unknown media and explicit empty choices both offer no selectable sizes;
// discovery and submission therefore cannot widen a route using platform defaults.
func creativeRouteImageSizes(route creativeModelRoute) []string {
	if route.Attributes.Media != nil && route.Attributes.Media.ImageSizes != nil {
		return append([]string(nil), route.Attributes.Media.ImageSizes...)
	}
	return nil
}

// CreativeCanonicalOption returns the first option spelling matching trimmed
// input without case sensitivity. Missing matches return empty and false so
// validation can reject input or select a permitted default.
func CreativeCanonicalOption(value string, options []string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), value) {
			return option, true
		}
	}
	return "", false
}

// CreativeContainsOption reports whether the supplied list contains a
// canonical match for value.
func CreativeContainsOption(options []string, value string) bool {
	_, ok := CreativeCanonicalOption(value, options)
	return ok
}

// CreativeDefaultOption returns the canonical preferred option, then first
// option, then empty. Chosen spelling comes from the supplied list so
// validated controls match capabilities sent to the browser.
func CreativeDefaultOption(options []string, preferred string) string {
	if CreativeContainsOption(options, preferred) {
		value, _ := CreativeCanonicalOption(preferred, options)
		return value
	}
	if len(options) > 0 {
		return options[0]
	}
	return ""
}

// CreativeModelsForGroup converts current resolved image routes to
// public-to-upstream model names. It evaluates finite candidates under
// group/provider rules, while returning lookup errors with the converted map
// to its caller.
// @project-doc docs/domains/creative_studio.md#creative_model_policy
func (s *Public) CreativeModelsForGroup(ctx context.Context, group *GroupView) (map[string]string, error) {
	routes, err := s.creativeModelRoutes(ctx, group)
	return creativeModelsFromRoutes(routes), err
}

// creativeModelsFromRoutes allocates request names mapped to the upstream
// names carried by their image routes.
func creativeModelsFromRoutes(routes map[string]creativeModelRoute) map[string]string {
	models := make(map[string]string, len(routes))
	for name, route := range routes {
		models[name] = route.Model
	}
	return models
}

// groupOperations returns platform operation union in CreativeOperationOrder
// using canonical membership checks. Input spelling can differ in case or
// surrounding whitespace, while output uses the stable product operation
// names.
func groupOperations(group *GroupView) []string {
	var operations []string
	for _, operation := range CreativeOperationOrder {
		for _, allowed := range group.Operations {
			if CreativeContainsOption(allowed, operation) {
				operations = append(operations, operation)
				break
			}
		}
	}
	return operations
}

// creativeModelRoutes prepares one group query and resolves its combined
// finite candidates. An unavailable query returns an allocated empty map,
// while provider preparation and model resolution failures remain errors.
func (s *Public) creativeModelRoutes(ctx context.Context, group *GroupView) (map[string]creativeModelRoute, error) {
	query, err := s.prepareCreativeModels(ctx, group)
	if err != nil || query == nil {
		return map[string]creativeModelRoute{}, err
	}
	configured := creativeSettingsModels(s.CreativeModelSettings(ctx), group.ID)
	return query.resolveModels(ctx, query.candidates(configured))
}

// CreateRun validates and moderates current scope, model permissions, and
// input before checking the trimmed idempotency key. Matching replay therefore
// still requires current admission to succeed.
//
// New tasks resolve price and hidden Key before metadata creation publishes
// provisioning recovery. Funds reservation, allowance marking, transient
// saves, and queue admission then advance separate saved phases. Later
// failures request release recovery for the persisted task rather than
// synchronously reversing every earlier cross-store action. Persistent hashes
// identify content while plaintext and media bytes enter temporary storage
// before task enqueue.
// @project-doc docs/domains/creative_studio.md#creative_task_lifecycle
func (s *Public) CreateRun(ctx context.Context, scope CreativeRunScope, params CreateCreativeRunParamsPublic, idempotencyKey string) (*CreativeRunPublic, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	userID := scope.UserID
	if !s.Enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	validated, err := s.ValidateCreateParams(ctx, userID, &params)
	if err != nil {
		return nil, err
	}
	if err := s.ModerateCreativeRequest(ctx, userID, validated); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.Repo.GetCreativeRunByIdempotencyKey(ctx, scope, idempotencyKey)
		if err == nil {
			if existing.RequestFingerprint != validated.Fingerprint {
				return nil, ErrCreativeRunIdempotencyConflict
			}
			out, err := s.Results.GetRunPublic(ctx, existing.RunID)
			if err != nil {
				return nil, err
			}
			out.IdempotentReplay = true
			return out, nil
		}
		if !errors.Is(err, ErrCreativeRunNotFound) {
			return nil, err
		}
	}
	pricing, err := s.ResolveCreativePricing(ctx, userID, validated)
	if err != nil {
		return nil, err
	}
	managedKey, err := s.EnsureKey(ctx, userID, validated.Group.ID)
	if err != nil {
		return nil, err
	}
	runID, err := NewCreativeRunID()
	if err != nil {
		return nil, err
	}
	holdAmount := pricing.EstimatedCost
	run, err := s.Repo.CreateCreativeRun(ctx, CreateCreativeRunParams{
		FrameCount: validated.FrameCount, FrameDurationMS: validated.FrameDurationMS,
		RunID:                      runID,
		UserID:                     userID,
		WorkspaceID:                scope.WorkspaceID,
		GroupID:                    validated.Group.ID,
		APIKeyID:                   managedKey,
		Model:                      validated.Model,
		RequestedModel:             params.Model,
		Operation:                  validated.Operation,
		Platform:                   validated.Platform,
		RequestedOutputCount:       validated.OutputCount,
		ImageSize:                  validated.ImageSize,
		AspectRatio:                validated.AspectRatio,
		ResponseMIMEType:           creativeRequestedMIME(validated.OutputFormat),
		PromptHash:                 validated.PromptHash,
		RequestFingerprint:         validated.Fingerprint,
		IdempotencyKey:             CreativeStringPtr(idempotencyKey),
		EstimatedCost:              pricing.EstimatedCost,
		HoldAmount:                 holdAmount,
		BaseUnitPrice:              pricing.BaseUnitPrice,
		SubscriptionRateMultiplier: pricing.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      pricing.BalanceRateMultiplier,
		PlanGroupRateEnabled:       pricing.PlanGroupRateEnabled,
	})
	if err != nil {
		return nil, err
	}
	telemetry.RecordRelatedRequest(ctx, telemetry.RequestRecord{RequestID: telemetry.StableRequestID(CreativeSettlementRequestID(run.RunID)), State: "running", Model: run.Model, StartedAt: run.CreatedAt, Aliases: []telemetry.RequestAlias{{Kind: "task", Value: run.RunID}, {Kind: "billing", Value: CreativeSettlementRequestID(run.RunID)}}})
	// Task creation already published provisioning recovery in its transaction.
	// Ensure the action before reserving funds so later failures retain a durable
	// task identifier and recovery path across these separate store operations.
	if err := s.Results.EnsureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxProvision); err != nil {
		return nil, err
	}
	// The task and provisioning action already exist before reservation. A later
	// creation failure therefore requests a persisted release target and recovery
	// action, because these funds, transient, and queue steps commit separately.
	if err := s.Results.Funding.Reserve(ctx, run); err != nil {
		s.FailRunAfterCreateError(ctx, run, "BILLING_HOLD_FAILED", err)
		return nil, err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, run.RunID, run.AllowanceReserved); err != nil {
			// Reserve is idempotent; retain the provisioning outbox
			// so recovery can persist the
			// reservation marker.
			return nil, err
		}
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseHoldReserved); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	s.Results.InvalidateCreativeAuthCache(ctx, userID)
	if err := s.SaveRunTransient(ctx, run, validated); err != nil {
		s.FailRunAfterCreateError(ctx, run, "TRANSIENT_SAVE_FAILED", err)
		return nil, ErrCreativeTransientFailed
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseTransientSaved); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	if s.Queue == nil {
		err := errors.New("creative queue is not configured")
		s.FailRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	// Queue admission can already represent this persisted task through inflight
	// bookkeeping. Accept that conflict before marking enqueued so provisioning
	// recovery does not turn an existing queue attempt into a creation failure.
	if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
		s.FailRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseEnqueued); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	out, err := s.Results.GetRunPublic(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FailRunAfterCreateError requests failed release recovery for a run and
// nonnil cause. A nil run returns directly; transition failures are logged
// while the caller retains the original creation error.
//
// The task creation transaction already recorded provisioning recovery before
// these separate funding, transient, or queue failures occurred. This helper
// first attempts a release_pending target, then ensures a release action on a
// best-effort basis. Its ignored ensure error cannot undo earlier store work,
// so a later reconciler still needs to inspect the persisted task and action
// state.
func (s *Public) FailRunAfterCreateError(ctx context.Context, run *CreativeRun, code string, cause error) {
	if run == nil {
		return
	}
	message := SanitizeCreativeMessage(cause.Error())
	if err := s.Repo.TransitionCreativeRunStatus(ctx, run.RunID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil {
		s.warn("creative.create_failure_mark_failed",
			"run_id", run.RunID,
			"error", err,
		)
	}
	// Recovery ensure is best effort after attempting the release target write.
	// Its failure is discarded here; the original task creation provision action
	// remains the durable evidence another reconciler can inspect.
	_ = s.Results.EnsureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxRelease)
}

// SaveRunTransient writes execution JSON, ordered source bytes, and an
// optional mask through separate temporary storage calls. Callers supply
// nonnil run and validated inputs, keeping their retained bytes stable until
// those synchronous writes finish.
//
// Payload metadata is saved before the media indexes it describes. A later
// source or mask failure can leave earlier keys present, and the error stops
// the sequence without rollback. Creation publishes transient_saved only after
// every required write succeeds, so recovery uses that phase to distinguish
// complete input provisioning from partial storage work.
func (s *Public) SaveRunTransient(ctx context.Context, run *CreativeRun, validated *ValidatedCreativeParams) error {
	if s.TransientStore == nil {
		return errors.New("creative transient store is not configured")
	}
	payload := &CreativeRunPayload{
		FrameCount: run.FrameCount, FrameDurationMS: run.FrameDurationMS,
		RunID:              run.RunID,
		UserID:             run.UserID,
		GroupID:            run.GroupID,
		APIKeyID:           run.APIKeyID,
		Model:              run.Model,
		Operation:          run.Operation,
		Prompt:             validated.Prompt,
		ImageSize:          run.ImageSize,
		AspectRatio:        run.AspectRatio,
		Background:         validated.Background,
		ThinkingLevel:      validated.ThinkingLevel,
		Quality:            validated.Quality,
		SourceCount:        len(validated.Sources),
		HasMask:            validated.Mask != nil,
		RequestFingerprint: run.RequestFingerprint,
	}
	// Save JSON before its ordered source and mask keys. These are separate
	// writes, so a partial failure leaves earlier values for creation recovery
	// while the transient_saved phase is published only after this complete
	// sequence.
	if err := s.TransientStore.SavePayload(ctx, run.RunID, payload); err != nil {
		return err
	}
	for i := range validated.Sources {
		if err := s.TransientStore.SaveInput(ctx, run.RunID, i, validated.Sources[i].Bytes); err != nil {
			return err
		}
	}
	if validated.Mask != nil {
		if err := s.TransientStore.SaveMask(ctx, run.RunID, validated.Mask.Bytes); err != nil {
			return err
		}
	}
	return nil
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

// ResolveCreativePricing returns image base price, separate funding-source
// rates, and estimated task amount. Missing unit quotes fail before
// reservation, while an explicit zero quote remains valid.
//
// The nonnegative group rate supplies subscription and default balance rates.
// A successful user override replaces balance rate, while read failure keeps
// the group default. A subscription quote can change the effective estimate
// multiplier while both source rates stay saved for later allocation.
// BillingModel selects quote identity separately from admission, and count
// scales the unit quote for the selected image size.
func (s *Public) ResolveCreativePricing(ctx context.Context, userID int64, validated *ValidatedCreativeParams) (*CreativePricingSnapshot, error) {
	group := validated.Group
	groupDefault := group.RateMultiplier
	if groupDefault < 0 {
		groupDefault = 0
	}
	subscriptionRate := groupDefault
	balanceRate := groupDefault
	if s.UserGroupRateRepo != nil {
		// An override read failure keeps the group balance rate for this quote.
		// The source-specific snapshot below still records subscription
		// and balance
		// rates separately from the effective amount estimated for reservation.
		if userRate, err := s.UserGroupRateRepo.GetByUserAndGroup(ctx, userID, group.ID); err == nil && userRate != nil {
			balanceRate = *userRate
		}
	}
	effective := balanceRate
	planGroupRateEnabled := true
	if s.SubscriptionMultiplier != nil {
		if value, ok := s.SubscriptionMultiplier(ctx, userID, group, groupDefault); ok {
			effective = value
		}
	}
	baseUnitPrice := 0.0
	estimatedCost := 0.0
	pricingModel := s.BillingModel(ctx, group, validated.Model, validated.FinalModel)
	if resolvedUnitPrice, ok := s.ImageUnitPrice(ctx, group, pricingModel, validated.ImageSize); ok {
		baseUnitPrice = resolvedUnitPrice
		estimatedCost = resolvedUnitPrice * float64(validated.OutputCount) * float64(max(1, validated.FrameCount)) * effective
	} else {
		return nil, billing.ErrImageTaskPricingMissing
	}
	return &CreativePricingSnapshot{
		BaseUnitPrice:              baseUnitPrice,
		SubscriptionRateMultiplier: subscriptionRate,
		BalanceRateMultiplier:      balanceRate,
		PlanGroupRateEnabled:       planGroupRateEnabled,
		EstimatedCost:              estimatedCost,
	}, nil
}

// BillingModel selects a linked pricing model or the final upstream identity
// used for image quotes. Empty upstream uses requested input first.
//
// A linked configuration keeps requested, mapped, and upstream names separate
// while BillingModelForPrice chooses its configured source. Missing mapping
// callbacks, nil groups, and unlinked configurations return upstream directly.
// This quote identity does not replace group/provider admission mapping, whose
// route is resolved independently before the task saves funding values.
func (s *Public) BillingModel(ctx context.Context, group *GroupView, requested, upstream string) string {
	if upstream == "" {
		upstream = requested
	}
	if s.GroupMapping == nil || group == nil {
		return upstream
	}
	mapping := s.GroupMapping(ctx, group.ID, requested)
	if mapping.PricingConfigID == 0 {
		return upstream
	}
	mapped := mapping.MappedModel
	if mapped == "" {
		mapped = requested
	}
	return routing.BillingModelForPrice(mapping, requested, mapped, upstream)
}

// intersectCreativeOperations returns left-side names in order when right has
// a canonical operation match.
func intersectCreativeOperations(left, right []string) []string {
	var result []string
	for _, value := range left {
		if CreativeContainsOption(right, value) {
			result = append(result, value)
		}
	}
	return result
}

// creativeRequestedMIME records the requested animation format without changing
// static defaults. Actual output MIME remains attached to each provider result
// and is checked before generated GIF work is settled.
func creativeRequestedMIME(format string) string {
	if format == "gif" {
		return "image/gif"
	}
	return DefaultCreativeResponseMime
}

// creativeGIFRange exposes shared animation bounds only for GIF-capable routes.
// Duration ranges use milliseconds and ten-millisecond steps; count ranges use
// whole frames. Other formats retain absent animation controls in the directory.
func creativeGIFRange(formats []string, duration bool) *CreativeNumericRange {
	if !slices.Contains(formats, "gif") {
		return nil
	}
	if duration {
		return &CreativeNumericRange{Min: protocol.GIFMinFrameDurationMS, Max: protocol.GIFMaxFrameDurationMS, Step: 10}
	}
	return &CreativeNumericRange{Min: protocol.GIFMinFrameCount, Max: protocol.GIFMaxFrameCount, Step: 1}
}
