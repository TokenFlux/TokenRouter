package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// configuration_providers.go manages provider instance settings and checks
// changes against unfinished payment references.
// Provider configuration changes retain omitted secrets while protecting
// recorded merchant identity for unfinished orders. Administrator listing
// removes only the known sensitive keys for an exact provider key, and new
// saves serialize plaintext JSON even when the previous record used historical
// ciphertext. Enabled saves also run adapter construction, whose eager
// validation differs by provider.
//
// Update first observes the instance, then can reread configuration for
// merging and memoize one in-progress count for several checks. Only a
// supplied Config adds an expected persisted-config comparison at the final
// store update; other counts and deletion checks remain separate observations.
// EasyPay method names carry prior localization content by stable IDs before
// mapping validation. Refund flags have a conditional cascade, and
// force-expired audit counts retain instances needed for late payment
// recovery. Registry refresh belongs to the HTTP/settings caller after
// persistence.

// EasyPay configuration keys identify explicit instance-level compatibility
// choices shared by administrator validation and the payment adapter.
const (
	// ConfigEasyPayAllowMerchantOrderNoAsTradeNo enables correction of an echoed
	// merchant number during the first verified payment confirmation only.
	ConfigEasyPayAllowMerchantOrderNoAsTradeNo = "allowMerchantOrderNoAsTradeNo"
)

// Provider configuration tables define recognized secret fields, in-progress
// protected identity fields, accepted provider keys and EasyPay custom code
// syntax for administrator configuration operations.
var (
	// ConfigProviderSensitiveConfigFields lists fields omitted from administrator
	// GET responses and retained when blank secret updates arrive. Provider keys
	// are exact, while field names are lowercased for lookup; unlisted fields
	// remain visible. Keep its recognized secret names aligned with frontend
	// providerConfig.ts sensitive metadata when changing the schema.
	ConfigProviderSensitiveConfigFields = map[string]map[string]struct{}{
		TypeEasyPay:   {"pkey": {}},
		TypeEPUSDT:    {"secretkey": {}},
		TypeAlipay:    {"privatekey": {}, "publickey": {}, "alipaypublickey": {}},
		TypeWxpay:     {"privatekey": {}, "apiv3key": {}, "publickey": {}},
		TypeStripe:    {"secretkey": {}, "webhooksecret": {}},
		TypeAirwallex: {"apikey": {}, "webhooksecret": {}},
	}

	// ConfigProviderPendingOrderProtectedConfigFields lists credentials and
	// merchant/currency identity compared before configuration updates with
	// in-progress orders. Comparison uses case-insensitive field lookup; the
	// separate count includes pending, processing, paid and recharging in the
	// PostgreSQL adapter.
	ConfigProviderPendingOrderProtectedConfigFields = map[string]map[string]struct{}{
		TypeEasyPay:   {"pkey": {}, "pid": {}},
		TypeEPUSDT:    {"secretkey": {}, "pid": {}, "currency": {}, "apibase": {}},
		TypeAlipay:    {"privatekey": {}, "publickey": {}, "alipaypublickey": {}, "appid": {}},
		TypeWxpay:     {"privatekey": {}, "apiv3key": {}, "publickey": {}, "appid": {}, "mpappid": {}, "mchid": {}, "publickeyid": {}, "certserial": {}},
		TypeStripe:    {"secretkey": {}, "webhooksecret": {}, "currency": {}},
		TypeAirwallex: {"clientid": {}, "apikey": {}, "webhooksecret": {}, "apibase": {}, "accountid": {}, "currency": {}},
	}

	// ConfigValidProviderKeys contains exact provider labels accepted by creation
	// request validation and the configured adapter factory choices.
	ConfigValidProviderKeys = map[string]bool{
		TypeEasyPay: true, TypeAlipay: true, TypeWxpay: true, TypeStripe: true, TypeAirwallex: true, TypeEPUSDT: true,
	}

	// ConfigEasyPayCustomMethodCodePattern accepts nonempty lowercase ASCII
	// letters, digits, underscores and hyphens for method and upstream mapping
	// codes.
	ConfigEasyPayCustomMethodCodePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

// ProviderInstanceResponse carries administrator instance fields with
// recognized secret keys omitted from configuration.
type ProviderInstanceResponse struct {
	ID int64 `json:"id"` // local provider instance identity.
	// stored payment adapter key.
	ProviderKey string `json:"provider_key"`
	// administrator instance display name.
	Name string `json:"name"`
	// Config is a newly decoded and filtered string map for administrator
	// display. Recoverable unreadable configuration yields nil, and only keys
	// recognized for this exact provider are omitted.
	Config map[string]string `json:"config"`
	// SupportedTypes is a newly split and trimmed method slice for administrator
	// display. Empty stored text yields an allocated empty slice; visible alias
	// normalization occurs in other consumers.
	SupportedTypes []string `json:"supported_types"`
	Limits         string   `json:"limits"` // stored channel limit JSON
	// stored checkout eligibility flag.
	Enabled bool `json:"enabled"`
	// stored administrator refund flag.
	RefundEnabled bool `json:"refund_enabled"`
	// stored buyer request eligibility.
	AllowUserRefund bool `json:"allow_user_refund"`
	// configured instance display order.
	SortOrder int `json:"sort_order"`
	// stored payment presentation mode.
	PaymentMode string `json:"payment_mode"`
}

// ConfigEasyPayCustomMethodConfig carries custom method mapping and stable
// localized display-name input.
type ConfigEasyPayCustomMethodConfig struct {
	// ID identifies prior display-name content during prepareMethodNames. Omitted
	// IDs are replaced by Type before duplicate-ID checks and localization
	// revision preparation.
	ID string `json:"id,omitempty"`
	// DisplayNameLocalization supplies versioned display content when present.
	// prepareMethodNames compares its prior ID content and writes the prepared
	// source into DisplayName before saving customMethods.
	DisplayNameLocalization *locale.Update[string] `json:"displayNameLocalization,omitempty"`
	// visible custom method code.
	Type string `json:"type"`
	// upstream EasyPay method code.
	UpstreamType string `json:"upstreamType"`
	// DisplayName is the plain custom label or prepared localization source.
	// Display lookup uses it only when localization is absent, while mapping
	// validation here does not independently require a plain name.
	DisplayName string `json:"displayName"`
}

// ConfigValidateProviderConfig invokes the runtime constructor with a
// validation instance label and returns its error. This helper always
// constructs; CreateProviderInstance and UpdateProviderInstance decide whether
// the final enabled state requires the call, and adapter constructors differ
// in eager credential parsing.
func (s *ConfigService) ConfigValidateProviderConfig(providerKey string, config map[string]string) error {
	_, err := s.runtime.CreateProvider(providerKey, "_validate_", config)
	return err
}

// ListProviderInstancesWithConfig returns sort-ordered administrator rows with
// split support and recognized secret fields omitted. Initial list errors
// abort; configuration interpretation can log recoverable failure and yield
// nil Config while other instance fields remain available.
func (s *ConfigService) ListProviderInstancesWithConfig(ctx context.Context) ([]ProviderInstanceResponse, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{SortByOrder: true})
	if err != nil {
		return nil, err
	}
	result := make([]ProviderInstanceResponse, 0, len(instances))
	for _, inst := range instances {
		resp := ProviderInstanceResponse{
			ID: inst.ID, ProviderKey: inst.ProviderKey, Name: inst.Name,
			SupportedTypes: ConfigSplitTypes(inst.SupportedTypes), Limits: inst.Limits,
			Enabled: inst.Enabled, RefundEnabled: inst.RefundEnabled, AllowUserRefund: inst.AllowUserRefund,
			SortOrder: inst.SortOrder, PaymentMode: inst.PaymentMode,
		}
		resp.Config = s.ConfigDecryptAndMaskConfig(inst.ProviderKey, inst.Config)
		result = append(result, resp)
	}
	return result, nil
}

// ConfigDecryptAndMaskConfig decodes stored configuration and returns a new
// scalar map omitting known secrets for the exact provider key.
// Nil/recoverably unreadable config returns nil; unrecognized provider/field
// keys remain in plaintext under the lookup rule.
func (s *ConfigService) ConfigDecryptAndMaskConfig(providerKey, stored string) map[string]string {
	cfg := s.ConfigDecryptConfig(stored)
	if cfg == nil {
		return nil
	}
	masked := make(map[string]string, len(cfg))
	// Filter only fields recognized by the exact provider's secret table.
	// Retained key spelling and values remain unchanged, so unknown fields are
	// still exposed in the administrator map.
	for k, v := range cfg {
		if ConfigIsSensitiveProviderConfigField(providerKey, k) {
			continue
		}
		masked[k] = v
	}
	return masked
}

// ConfigIsSensitiveProviderConfigField reports membership in the exact
// provider's secret-field set using a lowercased field name. It does not trim
// names or normalize provider spelling, so unknown keys are not treated as
// recognized secrets.
func ConfigIsSensitiveProviderConfigField(providerKey, fieldName string) bool {
	fields, ok := ConfigProviderSensitiveConfigFields[providerKey]
	if !ok {
		return false
	}
	_, found := fields[strings.ToLower(fieldName)]
	return found
}

// ConfigHasPendingOrderProtectedConfigChange reports whether any listed
// credential/identity field differs between the two maps. Provider lookup is
// exact and field reads are case-insensitive; missing fields read as empty and
// unknown providers report false.
func ConfigHasPendingOrderProtectedConfigChange(providerKey string, currentConfig, nextConfig map[string]string) bool {
	fields, ok := ConfigProviderPendingOrderProtectedConfigFields[providerKey]
	if !ok {
		return false
	}
	for fieldName := range fields {
		if ConfigProviderConfigFieldValue(currentConfig, fieldName) != ConfigProviderConfigFieldValue(nextConfig, fieldName) {
			return true
		}
	}
	return false
}

// ConfigProviderConfigFieldValue returns the first case-insensitive matching
// field value from the supplied map or empty text. Multiple case variants
// follow map iteration order; this helper does not trim or resolve
// duplicate-key ambiguity.
func ConfigProviderConfigFieldValue(config map[string]string, fieldName string) string {
	for key, value := range config {
		if strings.EqualFold(key, fieldName) {
			return value
		}
	}
	return ""
}

// ConfigCountPendingOrders delegates the instance's in-progress order count
// for update and deletion checks. The PostgreSQL adapter includes pending,
// processing, paid and recharging, and this observation is separate from later
// instance persistence.
func (s *ConfigService) ConfigCountPendingOrders(ctx context.Context, providerInstanceID int64) (int, error) {
	return s.store.CountInProgressByProvider(ctx, providerInstanceID)
}

// CreateProviderInstance validates an administrator draft and persists its
// instance fields with plaintext JSON configuration. Exact provider key and
// nonblank name are required; disabled drafts skip provider construction.
// EPUSDT requires its cryptocurrency method, selects hosted popup checkout,
// and rejects requests enabling automatic or buyer-initiated refunds.
//
// EasyPay name preparation mutates the supplied config map before custom
// mapping validation. Enabled requests construct an adapter, which can defer
// some credential parsing to later calls. AllowUserRefund is stored only when
// requested together with RefundEnabled. Store creation follows validation,
// while registry refresh belongs to the outer caller; limits/mode/support text
// retain the validation scope actually applied here.
func (s *ConfigService) CreateProviderInstance(ctx context.Context, req CreateProviderInstanceRequest) (*ProviderInstance, error) {
	typesStr := ConfigJoinTypes(req.SupportedTypes)
	if err := ConfigValidateProviderRequest(req.ProviderKey, req.Name, typesStr); err != nil {
		return nil, err
	}
	if req.ProviderKey == TypeEPUSDT {
		if req.RefundEnabled || req.AllowUserRefund {
			return nil, infraerrors.BadRequest("REFUND_UNSUPPORTED", "epusdt automatic refunds are not supported")
		}
		req.PaymentMode = "popup"
	}
	if req.ProviderKey == TypeEasyPay {
		if err := ConfigValidateEasyPayTradeNumberCompatibility(req.Config); err != nil {
			return nil, err
		}
		// Prepare stable display identities and localization revisions before custom
		// mapping checks and serialization. The supplied map can already be changed
		// if a later validation step rejects this draft.
		if err := prepareMethodNames(req.Config, nil); err != nil {
			return nil, err
		}
		if err := ConfigValidateEasyPayCustomMethods(req.Config, typesStr); err != nil {
			return nil, err
		}
	}
	if err := s.ConfigValidateVisibleMethodEnablementConflicts(ctx, 0, req.ProviderKey, typesStr, req.Enabled); err != nil {
		return nil, err
	}
	if req.Enabled {
		if err := s.ConfigValidateProviderConfig(req.ProviderKey, req.Config); err != nil {
			return nil, err
		}
	}
	enc, err := s.ConfigEncryptConfig(req.Config)
	if err != nil {
		return nil, err
	}
	allowUserRefund := req.AllowUserRefund && req.RefundEnabled
	return s.store.CreateInstance(ctx, ProviderInstance{
		ProviderKey:     req.ProviderKey,
		Name:            req.Name,
		Config:          enc,
		SupportedTypes:  typesStr,
		Enabled:         req.Enabled,
		PaymentMode:     req.PaymentMode,
		SortOrder:       req.SortOrder,
		Limits:          req.Limits,
		RefundEnabled:   req.RefundEnabled,
		AllowUserRefund: allowUserRefund,
	})
}

// TestProviderDraft constructs an unsaved exact EasyPay draft and calls
// QueryOrder with a newly supplied merchant candidate to report connectivity.
// Other provider keys are rejected; optional saved instance identity must have
// the same key.
//
// Saved-instance testing rereads and merges config so omitted/blank recognized
// secrets can be reused, while unsaved input remains supplied data.
// A recognized missing-order reply confirms this synthetic probe reached the
// channel. Constructor errors become invalid-config and other query errors
// become test-failed with causes retained. The query candidate is not persisted and this
// operation does not submit payment/refund, but it performs the external read
// through the adapter when invoked.
func (s *ConfigService) TestProviderDraft(ctx context.Context, req TestProviderDraftRequest) (*ProviderDraftTestResult, error) {
	providerKey := strings.TrimSpace(req.ProviderKey)
	if providerKey != TypeEasyPay {
		return nil, infraerrors.BadRequest("UNSUPPORTED_PROVIDER_TEST", "payment provider test is not supported")
	}

	config := req.Config
	if config == nil {
		config = map[string]string{}
	}
	if req.InstanceID != nil {
		inst, err := s.store.Instance(ctx, *req.InstanceID)
		if err != nil {
			if s.store.IsNotFound(err) {
				return nil, infraerrors.NotFound("PROVIDER_NOT_FOUND", "payment provider instance not found")
			}
			return nil, fmt.Errorf("load provider instance for test: %w", err)
		}
		if inst.ProviderKey != providerKey {
			return nil, infraerrors.BadRequest("PROVIDER_KEY_MISMATCH", "provider key does not match instance")
		}
		mergedConfig, mergeErr := s.ConfigMergeConfig(ctx, *req.InstanceID, config)
		if mergeErr != nil {
			return nil, mergeErr
		}
		config = mergedConfig
	}

	prov, err := s.runtime.CreateProvider(providerKey, "_draft_test_", config)
	if err != nil {
		return nil, infraerrors.BadRequest("PAYMENT_PROVIDER_TEST_INVALID_CONFIG", "payment provider test configuration is invalid").WithCause(err)
	}
	if _, err := prov.QueryOrder(ctx, s.runtime.NewTradeNo()); err != nil && !errors.Is(err, ErrProviderOrderNotFound) {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_TEST_FAILED", "payment provider test failed").WithCause(err)
	}
	return &ProviderDraftTestResult{Reachable: true}, nil
}

// ConfigValidateProviderRequest requires a trimmed nonblank display name and
// exact key in ConfigValidProviderKeys. EPUSDT requires its single hosted
// cryptocurrency method; other provider support rules are validated by their
// configuration-specific paths before persistence.
func ConfigValidateProviderRequest(providerKey, name, supportedTypes string) error {
	if strings.TrimSpace(name) == "" {
		return infraerrors.BadRequest("VALIDATION_ERROR", "provider name is required")
	}
	if providerKey == TypeEPUSDT && supportedTypes != TypeEPUSDT {
		return infraerrors.BadRequest("VALIDATION_ERROR", "epusdt supports only the cryptocurrency payment method")
	}
	if !ConfigValidProviderKeys[providerKey] {
		return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("invalid provider key: %s", providerKey))
	}
	return nil
}

// ConfigValidateEasyPayCustomMethods validates custom JSON mappings against
// the trimmed supported-method list. Nil/blank configuration supplies no
// custom mappings, while malformed JSON returns a validation error.
//
// Custom Type and UpstreamType need nonblank lowercase code syntax; Type
// cannot begin with Alipay/WeChat prefixes and duplicate custom types are
// rejected. Supported built-in Alipay/WeChat need no custom mapping, while
// each other support label needs a matching custom Type. Display
// names/localization and stable IDs are handled by prepareMethodNames
// separately, so this validator does not independently validate those fields
// or mutate the supplied JSON.
func ConfigValidateEasyPayCustomMethods(config map[string]string, supportedTypes string) error {
	if config == nil {
		config = map[string]string{}
	}
	raw := strings.TrimSpace(config["customMethods"])
	methods := make([]ConfigEasyPayCustomMethodConfig, 0)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &methods); err != nil {
			return infraerrors.BadRequest("VALIDATION_ERROR", "customMethods must be a JSON array")
		}
	}

	customTypes := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		method.Type = strings.TrimSpace(method.Type)
		method.UpstreamType = strings.TrimSpace(method.UpstreamType)
		if method.Type == "" || method.UpstreamType == "" {
			return infraerrors.BadRequest("VALIDATION_ERROR", "customMethods upstreamType is required")
		}
		if !ConfigEasyPayCustomMethodCodePattern.MatchString(method.Type) {
			return infraerrors.BadRequest("VALIDATION_ERROR", "customMethods type may only contain lowercase letters, digits, underscores, and hyphens")
		}
		if !ConfigEasyPayCustomMethodCodePattern.MatchString(method.UpstreamType) {
			return infraerrors.BadRequest("VALIDATION_ERROR", "customMethods upstreamType may only contain lowercase letters, digits, underscores, and hyphens")
		}
		if ConfigEasyPayCustomMethodTypeConflictsWithBuiltin(method.Type) {
			return infraerrors.BadRequest("VALIDATION_ERROR", "customMethods type cannot start with alipay or wxpay")
		}
		if _, exists := customTypes[method.Type]; exists {
			return infraerrors.BadRequest("VALIDATION_ERROR", "duplicate customMethods type")
		}
		customTypes[method.Type] = struct{}{}
	}

	for _, supportedType := range ConfigSplitTypes(supportedTypes) {
		supportedType = strings.TrimSpace(supportedType)
		if supportedType == "" || supportedType == TypeAlipay || supportedType == TypeWxpay {
			continue
		}
		if !ConfigEasyPayCustomMethodCodePattern.MatchString(supportedType) {
			return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("supported EasyPay custom type %s may only contain lowercase letters, digits, underscores, and hyphens", supportedType))
		}
		if _, exists := customTypes[supportedType]; !exists {
			return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("supported EasyPay custom type %s has no customMethods mapping", supportedType))
		}
	}
	return nil
}

// ConfigEasyPayCustomMethodTypeConflictsWithBuiltin reports Alipay/WeChat
// prefix conflicts for the supplied code text.
func ConfigEasyPayCustomMethodTypeConflictsWithBuiltin(methodType string) bool {
	return strings.HasPrefix(methodType, TypeAlipay) || strings.HasPrefix(methodType, TypeWxpay)
}

// UpdateProviderInstance applies supplied fields after current-instance,
// merged-config and in-progress observations. Omitted pointers/maps/slices
// generally retain stored fields, while a supplied empty collection participates
// in its corresponding update rules. EPUSDT always selects hosted popup mode
// and rejects requests enabling automatic or buyer-initiated refunds.
//
// One successful in-progress count is memoized for protected credential
// changes, disabling and support removal checks. Config merging rereads the
// instance and can prepare EasyPay localization against the initial current
// config. Final enabled state runs construction. Only supplied Config adds
// ExpectedConfig from the initial stored text, rejecting a changed config
// during final persistence; count observations do not share that update
// transaction. Refund-disabled stages buyer-refund false, while requested true
// is staged only when final refund permission is true. Caller refresh follows
// the persisted result.
func (s *ConfigService) UpdateProviderInstance(ctx context.Context, id int64, req UpdateProviderInstanceRequest) (*ProviderInstance, error) {
	current, err := s.store.Instance(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load provider instance: %w", err)
	}
	if current.ProviderKey == TypeEPUSDT {
		if req.RefundEnabled != nil && *req.RefundEnabled || req.AllowUserRefund != nil && *req.AllowUserRefund {
			return nil, infraerrors.BadRequest("REFUND_UNSUPPORTED", "epusdt automatic refunds are not supported")
		}
		req.PaymentMode = configPointer("popup")
	}
	var pendingOrderCount *int
	getPendingOrderCount := func() (int, error) {
		if pendingOrderCount != nil {
			return *pendingOrderCount, nil
		}
		count, err := s.ConfigCountPendingOrders(ctx, id)
		if err != nil {
			return 0, fmt.Errorf("check pending orders: %w", err)
		}
		// Reuse this successful count for the remaining checks in this request. It
		// remains an observation rather than a lock spanning the later store update,
		// and failures are not memoized.
		pendingOrderCount = &count
		return count, nil
	}
	nextEnabled := current.Enabled
	if req.Enabled != nil {
		nextEnabled = *req.Enabled
	}
	nextSupportedTypes := current.SupportedTypes
	if req.SupportedTypes != nil {
		nextSupportedTypes = ConfigJoinTypes(req.SupportedTypes)
	}
	if current.ProviderKey == TypeEPUSDT && nextSupportedTypes != TypeEPUSDT {
		return nil, infraerrors.BadRequest("VALIDATION_ERROR", "epusdt requires its cryptocurrency payment method")
	}
	if err := s.ConfigValidateVisibleMethodEnablementConflicts(ctx, id, current.ProviderKey, nextSupportedTypes, nextEnabled); err != nil {
		return nil, err
	}
	var mergedConfig map[string]string
	if req.Config != nil {
		currentConfig := s.ConfigDecryptConfig(current.Config)
		mergedConfig, err = s.ConfigMergeConfig(ctx, id, req.Config)
		if err != nil {
			return nil, err
		}
		if current.ProviderKey == TypeEasyPay {
			// Recover display-name revisions from the initial config while editing the
			// separately merged map. The expected persisted-config comparison later
			// rejects an intervening stored text change before accepting this prepared
			// update.
			if err := prepareMethodNames(mergedConfig, currentConfig); err != nil {
				return nil, err
			}
		}
		if ConfigHasPendingOrderProtectedConfigChange(current.ProviderKey, currentConfig, mergedConfig) {
			count, err := getPendingOrderCount()
			if err != nil {
				return nil, err
			}
			if count > 0 {
				return nil, infraerrors.Conflict("PENDING_ORDERS", "instance has pending orders").
					WithMetadata(map[string]string{"count": strconv.Itoa(count)})
			}
		}
	}
	if req.Enabled != nil && !*req.Enabled {
		count, err := getPendingOrderCount()
		if err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, infraerrors.Conflict("PENDING_ORDERS", "instance has pending orders").
				WithMetadata(map[string]string{"count": strconv.Itoa(count)})
		}
	}
	configToValidate := mergedConfig
	if configToValidate == nil {
		configToValidate = s.ConfigDecryptConfig(current.Config)
	}
	if current.ProviderKey == TypeEasyPay {
		if err := ConfigValidateEasyPayTradeNumberCompatibility(configToValidate); err != nil {
			return nil, err
		}
		if err := ConfigValidateEasyPayCustomMethods(configToValidate, nextSupportedTypes); err != nil {
			return nil, err
		}
	}
	// Construct the adapter only for the final enabled state, after merging the
	// actual configuration used by the save. Disabled drafts can retain
	// incomplete constructor requirements for later editing.
	finalEnabled := current.Enabled
	if req.Enabled != nil {
		finalEnabled = *req.Enabled
	}
	if finalEnabled {
		if err := s.ConfigValidateProviderConfig(current.ProviderKey, configToValidate); err != nil {
			return nil, err
		}
	}
	u := InstancePatch{}
	if req.Config != nil {
		// Carry the initial persisted config text into the conditional update only
		// for supplied config input. This rejects a changed config after merging
		// while other requested fields keep their separate patch rules.
		u.ExpectedConfig = &current.Config
	}
	if req.Name != nil {
		u.Name = configPointer(*req.Name)
	}
	if mergedConfig != nil {
		enc, err := s.ConfigEncryptConfig(mergedConfig)
		if err != nil {
			return nil, err
		}
		u.Config = configPointer(enc)
	}
	if req.SupportedTypes != nil {
		// An in-progress order can still need its stored payment method. Check the
		// memoized count before accepting removal, while added or retained exact
		// trimmed labels remain eligible.
		count, err := getPendingOrderCount()
		if err != nil {
			return nil, err
		}
		if count > 0 {
			// Compare requested support against the initial current row's comma labels.
			// This is not another row read, and trimming alone does not normalize
			// direct/visible aliases as equivalent.
			oldTypes := strings.Split(current.SupportedTypes, ",")
			newTypes := req.SupportedTypes
			for _, ot := range oldTypes {
				ot = strings.TrimSpace(ot)
				if ot == "" {
					continue
				}
				found := false
				for _, nt := range newTypes {
					if strings.TrimSpace(nt) == ot {
						found = true
						break
					}
				}
				if !found {
					return nil, infraerrors.Conflict("PENDING_ORDERS", "cannot remove payment types while instance has pending orders").
						WithMetadata(map[string]string{"count": strconv.Itoa(count)})
				}
			}
		}
		u.SupportedTypes = configPointer(ConfigJoinTypes(req.SupportedTypes))
	}
	if req.Enabled != nil {
		u.Enabled = configPointer(*req.Enabled)
	}
	if req.SortOrder != nil {
		u.SortOrder = configPointer(*req.SortOrder)
	}
	if req.Limits != nil {
		u.Limits = configPointer(*req.Limits)
	}
	if req.RefundEnabled != nil {
		u.RefundEnabled = configPointer(*req.RefundEnabled)
		// Disabling administrator refunds stages buyer-refund false in the same
		// patch. A later requested true is only staged when the final administrator
		// refund flag permits it.
		if !*req.RefundEnabled {
			u.AllowUserRefund = configPointer(false)
		}
	}
	if req.AllowUserRefund != nil {
		// A requested buyer-refund true depends on the supplied or current
		// administrator refund flag. If that flag is false, this request adds no
		// true patch; an explicit false still writes false.
		if *req.AllowUserRefund {
			refundEnabled := false
			if req.RefundEnabled != nil {
				refundEnabled = *req.RefundEnabled
			} else {
				refundEnabled = current.RefundEnabled
			}
			if refundEnabled {
				u.AllowUserRefund = configPointer(true)
			}
		} else {
			u.AllowUserRefund = configPointer(false)
		}
	}
	if req.PaymentMode != nil {
		u.PaymentMode = configPointer(*req.PaymentMode)
	}
	return s.store.UpdateInstance(ctx, id, u)
}

// ConfigValidateEasyPayTradeNumberCompatibility accepts the optional string
// boolean used by administrator saves and adapter construction. Missing or empty
// input disables compatibility. Other nonempty values must be exactly true or false.
func ConfigValidateEasyPayTradeNumberCompatibility(config map[string]string) error {
	switch config[ConfigEasyPayAllowMerchantOrderNoAsTradeNo] {
	case "", "true", "false":
		return nil
	default:
		return infraerrors.BadRequest("VALIDATION_ERROR", "allowMerchantOrderNoAsTradeNo must be true or false")
	}
}

// GetUserRefundEligibleInstanceIDs returns decimal instance IDs from the
// store's user-refund-eligible query. The PostgreSQL predicate requires both
// AllowUserRefund and RefundEnabled, independently of Enabled; callers still
// validate an individual order's historical binding and refund eligibility.
func (s *ConfigService) GetUserRefundEligibleInstanceIDs(ctx context.Context) ([]string, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{UserRefundEligible: true})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(instances))
	// Use decimal text to match saved provider-instance order bindings.
	for _, inst := range instances {
		ids = append(ids, strconv.FormatInt(inst.ID, 10))
	}
	return ids, nil
}

// ConfigMergeConfig rereads one instance, decodes its config and merges
// supplied scalar updates into newly decoded storage. Recoverable
// unreadable/empty config starts with a writable empty map.
//
// A blank recognized secret keeps its existing value, while blank unrecognized
// fields replace theirs. Sensitivity uses exact provider and lowercased field
// name; duplicate case spellings can remain separate map keys. This method
// does not persist or lock the instance, and UpdateProviderInstance separately
// compares its initial ExpectedConfig when config input is present.
func (s *ConfigService) ConfigMergeConfig(ctx context.Context, id int64, newConfig map[string]string) (map[string]string, error) {
	inst, err := s.store.Instance(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load existing provider: %w", err)
	}
	existing := s.ConfigDecryptConfig(inst.Config)
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range newConfig {
		// Blank recognized secret input means retain its current credential because
		// administrator listing omitted that value. Other submitted text, including
		// blank unrecognized fields, replaces its exact map key.
		if v == "" && ConfigIsSensitiveProviderConfigField(inst.ProviderKey, k) {
			continue
		}
		existing[k] = v
	}
	return existing, nil
}

// ConfigDecryptConfig reads plaintext or historical encrypted text and logs
// unreadable results as nil for configuration callers. Empty/JSON-null values
// can also return nil as readable; Decrypt's decoded-nonce precondition still
// applies to the historical format.
func (s *ConfigService) ConfigDecryptConfig(stored string) map[string]string {
	config, ok := parseProviderConfig(stored, s.encryptionKey)
	// Expose recoverable unreadable config as nil with a length-only parse
	// warning. Merge/list callers choose their empty or omitted representation
	// while valid old ciphertext still uses the historical key.
	if !ok {
		s.warn("payment provider config unreadable, treating as empty for re-entry",
			"stored_len", len(stored))
	}
	return config
}

// DeleteProviderInstance rejects deletion when in-progress orders or
// force-expired audit observations require the instance. Count errors return
// before deletion, with distinct conflicts for the two retained order classes.
//
// The two counts and final delete are separate store calls rather than one
// locked transaction. Force-expired counting joins initially expired
// instance-bound IDs to mandatory action history so credentials can remain for
// late payment. A later concurrent state change can alter those observations;
// this method adds no reservation. Registry refresh remains the outer caller's
// post-persistence operation.
func (s *ConfigService) DeleteProviderInstance(ctx context.Context, id int64) error {
	count, err := s.ConfigCountPendingOrders(ctx, id)
	if err != nil {
		return fmt.Errorf("check pending orders: %w", err)
	}
	if count > 0 {
		return infraerrors.Conflict("PENDING_ORDERS",
			fmt.Sprintf("this instance has %d in-progress orders and cannot be deleted", count))
	}
	forcedExpiredCount, err := s.ConfigCountForcedExpiredOrders(ctx, id)
	if err != nil {
		return fmt.Errorf("check force expired orders: %w", err)
	}
	if forcedExpiredCount > 0 {
		return infraerrors.Conflict("FORCED_EXPIRED_ORDERS",
			fmt.Sprintf("this instance has %d force-expired orders and must remain available for late payment recovery", forcedExpiredCount)).
			WithMetadata(map[string]string{"count": strconv.Itoa(forcedExpiredCount)})
	}
	return s.store.DeleteInstance(ctx, id)
}

// ConfigCountForcedExpiredOrders delegates the instance retention count used
// before provider deletion. The PostgreSQL adapter selects expired order IDs
// then counts their force-expiration audits in a second observation, returning
// any storage error.
func (s *ConfigService) ConfigCountForcedExpiredOrders(ctx context.Context, providerInstanceID int64) (int, error) {
	return s.store.CountForcedExpiredByProvider(ctx, providerInstanceID)
}

// ConfigEncryptConfig serializes the supplied scalar config map as plaintext
// JSON for persistence. Nil input becomes JSON null, and marshal errors return
// with context; historical encrypted reads remain supported by
// ConfigDecryptConfig separately.
func (s *ConfigService) ConfigEncryptConfig(cfg map[string]string) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(data), nil
}
