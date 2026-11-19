package creative

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// public_catalog.go lists user-accessible Creative Studio models, discovers
// administrator model candidates, and resolves route capabilities and display
// quotes.
// User discovery evaluates administrator allowlist entries against active
// group permissions, provider scheduling state, and first-tier quote
// availability, while administrator candidate discovery combines finite
// configured names across providers and groups without user-binding filters.
//
// Image capabilities determine size, aspect-ratio, quality, background,
// thinking, and per-operation animation options independently from price
// overrides so a generation GIF route cannot grant animation support to edit
// or inpaint operations that lack an eligible route.

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
		media = modelrules.MediaForModel(platform, model)
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
