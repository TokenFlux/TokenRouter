// 支付配置与管理规则的唯一实现；存储、环境和渠道构造由端口提供。
package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// ConfigValidateProviderConfig runs the provider's constructor to surface config-level
// errors at save time (e.g. wxpay missing certSerial), instead of only failing
// when an order is created. Returns the structured ApplicationError from the
// constructor so the frontend i18n layer can localize it.
//
// Only validates enabled instances — a disabled instance may be a half-filled
// draft the admin will complete later.
func (s *ConfigService) ConfigValidateProviderConfig(providerKey string, config map[string]string) error {
	_, err := s.runtime.CreateProvider(providerKey, "_validate_", config)
	return err
}

func (s *ConfigService) ListProviderInstances(ctx context.Context) ([]*ProviderInstance, error) {
	return s.store.ListInstances(ctx, InstanceFilter{SortByOrder: true})
}

// ProviderInstanceResponse is the API response for a provider instance.
type ProviderInstanceResponse struct {
	ID              int64             `json:"id"`
	ProviderKey     string            `json:"provider_key"`
	Name            string            `json:"name"`
	Config          map[string]string `json:"config"`
	SupportedTypes  []string          `json:"supported_types"`
	Limits          string            `json:"limits"`
	Enabled         bool              `json:"enabled"`
	RefundEnabled   bool              `json:"refund_enabled"`
	AllowUserRefund bool              `json:"allow_user_refund"`
	SortOrder       int               `json:"sort_order"`
	PaymentMode     string            `json:"payment_mode"`
}

// ListProviderInstancesWithConfig returns provider instances with decrypted config.
func (s *ConfigService) ListProviderInstancesWithConfig(ctx context.Context) ([]ProviderInstanceResponse, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{SortByOrder: true})
	if err != nil {
		return nil, err
	}
	result := make([]ProviderInstanceResponse, 0, len(instances))
	for _, inst := range instances {
		resp := ProviderInstanceResponse{
			ID: int64(inst.ID), ProviderKey: inst.ProviderKey, Name: inst.Name,
			SupportedTypes: ConfigSplitTypes(inst.SupportedTypes), Limits: inst.Limits,
			Enabled: inst.Enabled, RefundEnabled: inst.RefundEnabled, AllowUserRefund: inst.AllowUserRefund,
			SortOrder: inst.SortOrder, PaymentMode: inst.PaymentMode,
		}
		resp.Config, err = s.ConfigDecryptAndMaskConfig(inst.ProviderKey, inst.Config)
		if err != nil {
			return nil, fmt.Errorf("decrypt config for instance %d: %w", inst.ID, err)
		}
		result = append(result, resp)
	}
	return result, nil
}

// ConfigDecryptAndMaskConfig returns the stored config with sensitive fields omitted.
// Admin UIs display masked placeholders for these; the raw values never leave
// the server. Callers that need the full config (e.g. payment runtime) must
// use ConfigDecryptConfig directly.
func (s *ConfigService) ConfigDecryptAndMaskConfig(providerKey, encrypted string) (map[string]string, error) {
	cfg, err := s.ConfigDecryptConfig(encrypted)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	masked := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if ConfigIsSensitiveProviderConfigField(providerKey, k) {
			continue
		}
		masked[k] = v
	}
	return masked, nil
}

// ConfigPendingOrderStatuses are order statuses considered "in progress".
var ConfigPendingOrderStatuses = []string{
	OrderStatusPending,
	OrderStatusProcessing,
	OrderStatusPaid,
	OrderStatusRecharging,
}

// ConfigProviderSensitiveConfigFields is the authoritative list of config keys that
// are treated as secrets per provider. Must stay in sync with the frontend
// definition at frontend/src/components/payment/providerConfig.ts
// (PROVIDER_CONFIG_FIELDS, fields with sensitive: true).
//
// Key matching is case-insensitive. Non-listed keys (e.g. appId, notifyUrl,
// stripe publishableKey) are returned in plaintext by the admin GET API.
var ConfigProviderSensitiveConfigFields = map[string]map[string]struct{}{
	TypeEasyPay:   {"pkey": {}},
	TypeAlipay:    {"privatekey": {}, "publickey": {}, "alipaypublickey": {}},
	TypeWxpay:     {"privatekey": {}, "apiv3key": {}, "publickey": {}},
	TypeStripe:    {"secretkey": {}, "webhooksecret": {}},
	TypeAirwallex: {"apikey": {}, "webhooksecret": {}},
}

// ConfigProviderPendingOrderProtectedConfigFields lists config keys that cannot be
// changed while the instance has in-progress orders. This includes secrets plus
// all provider identity fields that are snapshotted into orders or used by
// webhook/refund verification.
var ConfigProviderPendingOrderProtectedConfigFields = map[string]map[string]struct{}{
	TypeEasyPay:   {"pkey": {}, "pid": {}},
	TypeAlipay:    {"privatekey": {}, "publickey": {}, "alipaypublickey": {}, "appid": {}},
	TypeWxpay:     {"privatekey": {}, "apiv3key": {}, "publickey": {}, "appid": {}, "mpappid": {}, "mchid": {}, "publickeyid": {}, "certserial": {}},
	TypeStripe:    {"secretkey": {}, "webhooksecret": {}, "currency": {}},
	TypeAirwallex: {"clientid": {}, "apikey": {}, "webhooksecret": {}, "apibase": {}, "accountid": {}, "currency": {}},
}

func ConfigIsSensitiveProviderConfigField(providerKey, fieldName string) bool {
	fields, ok := ConfigProviderSensitiveConfigFields[providerKey]
	if !ok {
		return false
	}
	_, found := fields[strings.ToLower(fieldName)]
	return found
}

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

func ConfigProviderConfigFieldValue(config map[string]string, fieldName string) string {
	for key, value := range config {
		if strings.EqualFold(key, fieldName) {
			return value
		}
	}
	return ""
}

func (s *ConfigService) ConfigCountPendingOrders(ctx context.Context, providerInstanceID int64) (int, error) {
	return s.store.CountInProgressByProvider(ctx, providerInstanceID)
}

var ConfigValidProviderKeys = map[string]bool{
	TypeEasyPay: true, TypeAlipay: true, TypeWxpay: true, TypeStripe: true, TypeAirwallex: true,
}

func (s *ConfigService) CreateProviderInstance(ctx context.Context, req CreateProviderInstanceRequest) (*ProviderInstance, error) {
	typesStr := ConfigJoinTypes(req.SupportedTypes)
	if err := ConfigValidateProviderRequest(req.ProviderKey, req.Name, typesStr); err != nil {
		return nil, err
	}
	if req.ProviderKey == TypeEasyPay {
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

// TestProviderDraft 使用未持久化的 EasyPay 草稿执行只读查单探测。
// 探测订单号不会写入本地或发起任何真实扣款。
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
	if _, err := prov.QueryOrder(ctx, s.runtime.NewTradeNo()); err != nil {
		return nil, infraerrors.ServiceUnavailable("PAYMENT_PROVIDER_TEST_FAILED", "payment provider test failed").WithCause(err)
	}
	return &ProviderDraftTestResult{Reachable: true}, nil
}

func ConfigValidateProviderRequest(providerKey, name, supportedTypes string) error {
	if strings.TrimSpace(name) == "" {
		return infraerrors.BadRequest("VALIDATION_ERROR", "provider name is required")
	}
	if !ConfigValidProviderKeys[providerKey] {
		return infraerrors.BadRequest("VALIDATION_ERROR", fmt.Sprintf("invalid provider key: %s", providerKey))
	}
	// supported_types can be empty (provider accepts no payment types until configured)
	return nil
}

var ConfigEasyPayCustomMethodCodePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

type ConfigEasyPayCustomMethodConfig struct {
	Type         string `json:"type"`
	UpstreamType string `json:"upstreamType"`
	DisplayName  string `json:"displayName"`
}

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

func ConfigEasyPayCustomMethodTypeConflictsWithBuiltin(methodType string) bool {
	return strings.HasPrefix(methodType, TypeAlipay) || strings.HasPrefix(methodType, TypeWxpay)
}

// UpdateProviderInstance updates a provider instance by ID (patch semantics).
// NOTE: This function exceeds 30 lines due to per-field nil-check patch update
// boilerplate and pending-order safety checks.
func (s *ConfigService) UpdateProviderInstance(ctx context.Context, id int64, req UpdateProviderInstanceRequest) (*ProviderInstance, error) {
	current, err := s.store.Instance(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load provider instance: %w", err)
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
	if err := s.ConfigValidateVisibleMethodEnablementConflicts(ctx, id, current.ProviderKey, nextSupportedTypes, nextEnabled); err != nil {
		return nil, err
	}
	var mergedConfig map[string]string
	if req.Config != nil {
		currentConfig, err := s.ConfigDecryptConfig(current.Config)
		if err != nil {
			return nil, fmt.Errorf("decrypt existing config: %w", err)
		}
		mergedConfig, err = s.ConfigMergeConfig(ctx, id, req.Config)
		if err != nil {
			return nil, err
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
		configToValidate, err = s.ConfigDecryptConfig(current.Config)
		if err != nil {
			return nil, fmt.Errorf("decrypt existing config: %w", err)
		}
	}
	if current.ProviderKey == TypeEasyPay {
		if err := ConfigValidateEasyPayCustomMethods(configToValidate, nextSupportedTypes); err != nil {
			return nil, err
		}
	}
	// Validate merged config when the instance will end up enabled.
	// This surfaces provider-level errors (e.g. wxpay missing certSerial) at save time,
	// so admins see them in the dialog instead of only when an order is created.
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
		// Check pending orders before removing payment types
		count, err := getPendingOrderCount()
		if err != nil {
			return nil, err
		}
		if count > 0 {
			// Load current instance to compare types
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
		// Cascade: turning off refund_enabled also disables allow_user_refund
		if !*req.RefundEnabled {
			u.AllowUserRefund = configPointer(false)
		}
	}
	if req.AllowUserRefund != nil {
		// Only allow enabling when refund_enabled is (or will be) true
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

// GetUserRefundEligibleInstanceIDs returns provider instance IDs that allow user refund.
func (s *ConfigService) GetUserRefundEligibleInstanceIDs(ctx context.Context) ([]string, error) {
	instances, err := s.store.ListInstances(ctx, InstanceFilter{UserRefundEligible: true})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, strconv.FormatInt(int64(inst.ID), 10))
	}
	return ids, nil
}

func (s *ConfigService) ConfigMergeConfig(ctx context.Context, id int64, newConfig map[string]string) (map[string]string, error) {
	inst, err := s.store.Instance(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load existing provider: %w", err)
	}
	existing, err := s.ConfigDecryptConfig(inst.Config)
	if err != nil {
		return nil, fmt.Errorf("decrypt existing config for instance %d: %w", id, err)
	}
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range newConfig {
		// Preserve existing secrets when the client submits an empty value
		// (admin UI omits the value to indicate "leave unchanged").
		if v == "" && ConfigIsSensitiveProviderConfigField(inst.ProviderKey, k) {
			continue
		}
		existing[k] = v
	}
	return existing, nil
}

// ConfigDecryptConfig parses a stored provider config.
// New records are plaintext JSON; legacy records are AES-256-GCM ciphertext
// ("iv:authTag:ciphertext"). Values that cannot be parsed as either — including
// legacy ciphertext with no/invalid TOTP_ENCRYPTION_KEY — are treated as empty,
// letting the admin re-enter the config via the UI to complete the migration.
//
// TODO(deprecated-legacy-ciphertext): The AES fallback branch is a transitional
// shim for pre-plaintext records. Remove it (and the encryptionKey field) after
// a few releases once all live deployments have re-saved their provider configs.
func (s *ConfigService) ConfigDecryptConfig(stored string) (map[string]string, error) {
	if stored == "" {
		return nil, nil
	}
	var cfg map[string]string
	if err := json.Unmarshal([]byte(stored), &cfg); err == nil {
		return cfg, nil
	}
	// Deprecated: legacy AES-256-GCM ciphertext fallback — scheduled for removal.
	if len(s.encryptionKey) == AES256KeySize {
		//nolint:staticcheck // SA1019: intentional legacy fallback, scheduled for removal
		if plaintext, err := Decrypt(stored, s.encryptionKey); err == nil {
			if err := json.Unmarshal([]byte(plaintext), &cfg); err == nil {
				return cfg, nil
			}
		}
	}
	s.warn("payment provider config unreadable, treating as empty for re-entry",
		"stored_len", len(stored))
	return nil, nil
}

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

func (s *ConfigService) ConfigCountForcedExpiredOrders(ctx context.Context, providerInstanceID int64) (int, error) {
	return s.store.CountForcedExpiredByProvider(ctx, providerInstanceID)
}

// ConfigEncryptConfig serialises a provider config for storage.
// New records are written as plaintext JSON; the historical AES-GCM wrapping
// has been dropped but ConfigDecryptConfig still accepts old ciphertext during migration.
func (s *ConfigService) ConfigEncryptConfig(cfg map[string]string) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(data), nil
}
