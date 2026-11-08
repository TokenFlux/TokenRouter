package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/bstats"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	paymentcore "github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// settings_composite_runtime_test.go verifies combined settings HTTP updates
// with omitted fields, explicit clearing, validation rejection, write failure,
// and committed data whose later
// application fails. These scenarios inspect stored strings, response values,
// reader order, notification timing, and security transitions across
// module-owned settings with controlled failure fixtures.
//
// The shared fixture uses production preparation and registry behavior but
// skips plan lookup and payment channel refresh. Response sentinels cover every
// flattened DTO field across GET and PUT, including optional and secret
// omissions. Separate cases exercise WS null patches, identity grants, payment
// source normalization, DingTalk policy, creative operations, captcha
// credentials, step-up transitions, and forwarded-IP trust after malformed
// historical input.

// settingAtomicRepo counts write attempts and injects persistence or later
// reload failure.
type settingAtomicRepo struct {
	*settingHandlerRepoStub // shared string settings fixture
	// writes counts attempts before failWrite is checked. failWrite rejects the
	// batch; failApply rejects later GetAll after any attempt, letting HTTP tests locate persistence versus application failure.
	writes    int  // batch write attempts
	failWrite bool // injected prewrite rejection
	failApply bool // injected postattempt reload failure
}

// failingAuthSourceSettingsRepoStub rejects batches containing the email-source
// balance key.
type failingAuthSourceSettingsRepoStub struct {
	values map[string]string // stored setting values
	// err is returned unchanged for a batch containing the email-source balance
	// key. Nil reports success while that branch still applies no keys.
	err error // selected batch failure
}

// creativeModelCandidateReaderStub supplies shared candidates and optional
// group-twelve operation filtering.
type creativeModelCandidateReaderStub struct {
	candidates []creative.CreativeModelCandidate // configured creative candidates
	// sanitizeGemini chooses the fixture's group-twelve inpaint filter after
	// normalization. False returns the input slice unchanged, retaining the caller's values and references.
	sanitizeGemini bool
}

// settingsResponseSource records calls and supplies the configured
// response-stage snapshot.
type settingsResponseSource struct {
	*composite.Runtime // actual preparation and reads
	// snapshot replaces Runtime reads when reads reaches responseRead, while
	// fastPolicy supplies response-only policy data. Both returned pointers remain owned by the scenario for its assertions.
	snapshot     *composite.Snapshot                  // configured response settings
	fastPolicy   *tierpolicy.OpenAIFastPolicySettings // configured Fast response
	responseRead int                                  // first fixture snapshot read
	reads        int                                  // attempted settings reads
	// calls is shared with payment and monitoring fixtures and appended before
	// every selected read. Tests keep the pointer initialized while synchronous response assembly records its order.
	calls *[]string
}

// settingsResponsePayment records payment reads and returns configured data
// plus error.
type settingsResponsePayment struct {
	// config and err are returned together, including a nonnil config on error.
	// calls records this read in the response fixture's shared ordered list before either value is returned.
	config *paymentcore.PaymentConfig // configured payment snapshot
	err    error                      // configured payment failure
	calls  *[]string                  // shared response call order
}

// settingsResponseMonitoring records read order while reporting disabled
// runtime monitoring.
type settingsResponseMonitoring struct {
	calls *[]string // shared monitoring call order
}

// TestSettingsResponsesWSPatchAndNull patches WS timeout to null and a
// connection limit to explicit zero. It checks removal versus retained zero,
// effective response data, and clearing the whole override object with null.
func TestSettingsResponsesWSPatchAndNull(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{ws.SettingKey: `{"read_timeout_seconds":45,"max_ingress_connections_per_api_key":5}`})
	response := doUpdateSettings(t, h, map[string]any{"responses_ws": map[string]any{"read_timeout_seconds": nil, "max_ingress_connections_per_api_key": 0}}, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"max_ingress_connections_per_api_key":0}`, repo.values[ws.SettingKey])
	require.Contains(t, response.Body.String(), `"responses_ws_effective"`)
	response = doUpdateSettings(t, h, map[string]any{"responses_ws": nil}, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{}`, repo.values[ws.SettingKey])
}

// TestSettingsResponsesWSInvalidPatchDoesNotWrite submits max_conns_per_provider
// one over a stored forty-five-second timeout override. It checks BadRequest
// and that the stored timeout JSON remains unchanged after the rejected patch.
func TestSettingsResponsesWSInvalidPatchDoesNotWrite(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{ws.SettingKey: `{"read_timeout_seconds":45}`})
	response := doUpdateSettings(t, h, map[string]any{"responses_ws": map[string]any{"max_conns_per_provider": 1}}, nil)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.JSONEq(t, `{"read_timeout_seconds":45}`, repo.values[ws.SettingKey])
}

// TestSettingsResponseFieldsAndPaymentErrors serves GET and PUT responses with
// configured, absent, nil, and failed payment readers. It checks reader order,
// GET/PUT error differences, omitted secrets, optional fields, and every
// expected flattened response value.
func TestSettingsResponseFieldsAndPaymentErrors(t *testing.T) {
	for _, paymentCase := range []string{"configured", "absent service", "nil config", "read error", "config with error"} {
		t.Run(paymentCase, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				t.Run(method, func(t *testing.T) {
					repo := &settingHandlerRepoStub{values: map[string]string{}}
					options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
					base, ok := options.Settings.(*composite.Runtime)
					require.True(t, ok)
					authDefaults, err := base.GetAuthSourceDefaultSettings(context.Background())
					require.NoError(t, err)
					snapshot := &composite.Snapshot{}
					fillSettingsResponseScalars(snapshot)
					snapshot.CustomMenuItems = `[{"id":"menu-1","label":"Help","icon_svg":"","url":"/help","visibility":"user","sort_order":2}]`
					snapshot.CustomEndpoints = `[{"id":"endpoint-1","name":"API","endpoint":"https://api.example.com","description":"Example"}]`
					snapshot.FooterLinks = `[{"id":"group-1","title":"Links","links":[{"id":"link-1","label":"Help","url":"/help"}]}]`
					snapshot.HomeFeaturedModels = `["example-model"]`
					snapshot.ResponsesWS = json.RawMessage(`{"read_timeout_seconds":45}`)
					snapshot.ProviderSchedulingThresholds = map[string]int{"openai": 75}
					snapshot.ProviderQuotaNotifyEmails = []identity.NotifyEmailEntry{{Email: "quota@example.com", Verified: true}}
					if paymentCase == "configured" {
						snapshot.DefaultSubscriptions = []billing.DefaultSubscriptionSetting{{PlanID: 17}}
					}
					var calls []string
					source := &settingsResponseSource{Runtime: base, snapshot: snapshot, responseRead: 1, calls: &calls}
					if paymentCase == "configured" {
						source.fastPolicy = &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{ServiceTier: "priority", Action: "filter", Scope: "all", UserIDs: []int64{42}}}}
					}
					if method == http.MethodPut {
						source.responseRead = 2
					}
					options.Settings = source
					options.Monitoring = &settingsResponseMonitoring{calls: &calls}
					payment := &settingsResponsePayment{calls: &calls}
					if paymentCase == "configured" || paymentCase == "config with error" {
						payment.config = &paymentcore.PaymentConfig{}
						fillSettingsResponseScalars(payment.config)
						payment.config.EnabledTypes = []string{"alipay", "wxpay"}
						payment.config.MethodFees = paymentcore.MethodFeeSettings{"alipay": {Enabled: true, FixedFee: 0.25, FeeRate: 1.5}}
					}
					if strings.Contains(paymentCase, "error") {
						payment.err = errors.New("payment read failed")
					}
					if paymentCase != "absent service" {
						options.Payment = payment
					}
					h := settingshttp.NewHandler(options)
					var rec *httptest.ResponseRecorder
					if method == http.MethodPut {
						rec = doUpdateSettings(t, h, map[string]any{}, nil)
					} else {
						rec = httptest.NewRecorder()
						ctx, _ := gin.CreateTestContext(rec)
						ctx.Request = httptest.NewRequest(http.MethodGet, "/settings", nil)
						h.GetSettings(ctx)
					}
					wantCalls := []string{"settings", "auth", "monitoring"}
					if method == http.MethodPut {
						wantCalls = []string{"settings", "auth", "settings", "auth"}
					}
					if paymentCase != "absent service" {
						wantCalls = append(wantCalls, "payment")
					}
					if method == http.MethodPut && payment.err != nil {
						require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
						require.Equal(t, wantCalls, calls)
						return
					}
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					require.Equal(t, append(wantCalls, "totp", "fast"), calls)
					var response struct {
						Data map[string]json.RawMessage `json:"data"` // decoded combined response values
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
					for _, field := range []string{"smtp_password", "turnstile_secret_key", "oidc_connect_client_secret"} {
						require.NotContains(t, response.Data, field)
					}
					assertSettingsResponseFields(t, response.Data, source, payment.config, authDefaults, method)
				})
			}
		})
	}
}

// fillSettingsResponseScalars writes distinct boolean, string, int, and float64
// sentinels into a pointed struct. The pointer and fields must be usable by
// reflection; other field kinds remain unchanged for scenario-specific setup.
func fillSettingsResponseScalars(target any) {
	value := reflect.ValueOf(target).Elem()
	for i := range value.NumField() {
		field := value.Field(i)
		switch field.Kind() {
		case reflect.Bool:
			field.SetBool(true)
		case reflect.String:
			field.SetString("fixture." + value.Type().Field(i).Name)
		case reflect.Int:
			// Index-based sentinels expose conversion from the wrong DTO source field.
			field.SetInt(int64(i + 10))
		case reflect.Float64:
			// Fractional sentinels keep float conversion distinct from integral fields.
			field.SetFloat(float64(i) + 0.25)
		}
	}
}

// assertSettingsResponseFields compares every flattened response member against
// its settings, payment, or auth-source fixture value. It consumes data by
// deleting checked keys, so callers pass a fresh decoded map for one response;
// nil payment selects zero-valued payment options.
//
// JSON-text members are compared as raw JSON, absent optional Fast/PUT
// threshold fields are checked separately, and nil subscription/fee values use
// their exposed empty collections. After auth-source fields and force-email are
// checked, any remaining key fails the assertion, exposing unowned response
// additions.
func assertSettingsResponseFields(t *testing.T, data map[string]json.RawMessage, source *settingsResponseSource, payment *paymentcore.PaymentConfig, authDefaults *identity.AuthSourceDefaultSettings, method string) {
	t.Helper()
	snapshot := source.snapshot
	if payment == nil {
		payment = &paymentcore.PaymentConfig{}
	}
	settingsValue := reflect.ValueOf(snapshot).Elem()
	paymentValue := reflect.ValueOf(payment).Elem()
	responseType := reflect.TypeFor[settingsdto.SystemSettings]()
	for field := range responseType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if (field.Name == "OpenAIFastPolicySettings" && source.fastPolicy == nil) || (field.Name == "ProviderSchedulingThresholds" && method == http.MethodPut) {
			require.NotContains(t, data, name)
			continue
		}
		var expected any
		switch field.Name {
		case "OpenAIFastPolicySettings":
			expected = source.fastPolicy
		case "TotpEncryptionKeyConfigured":
			expected = true
		case "OpsMonitoringEnabled":
			expected = method == http.MethodPut
		case "DefaultSubscriptions":
			expected = snapshot.DefaultSubscriptions
			if snapshot.DefaultSubscriptions == nil {
				expected = []any{}
			}
		case "LoginAgreementDocuments":
			expected = []any{}
		case "CustomMenuItems", "CustomEndpoints", "FooterLinks", "HomeFeaturedModels":
			expected = json.RawMessage(settingsValue.FieldByName(field.Name).String())
		case "PaymentMethodFees":
			expected = payment.MethodFees
			if payment.MethodFees == nil {
				expected = map[string]any{}
			}
		default:
			value := settingsValue.FieldByName(field.Name)
			if !value.IsValid() {
				paymentName := strings.TrimPrefix(field.Name, "Payment")
				// This response uses the shortened payment suffix while the source
				// field spells out Strategy. Resolve that actual identity before reflecting its expected value.
				if field.Name == "PaymentLoadBalanceStrat" {
					paymentName = "LoadBalanceStrategy"
				}
				value = paymentValue.FieldByName(paymentName)
			}
			require.True(t, value.IsValid(), field.Name)
			expected = value.Interface()
		}
		encoded, err := json.Marshal(expected)
		require.NoError(t, err)
		require.Contains(t, data, name)
		require.JSONEq(t, string(encoded), string(data[name]), name)
		delete(data, name)
	}
	for _, provider := range []struct {
		name  string                                // flattened auth-source prefix
		value identity.ProviderDefaultGrantSettings // expected provider grants
	}{
		{"email", authDefaults.Email},
		{"linuxdo", authDefaults.LinuxDo},
		{"oidc", authDefaults.OIDC},
		{"wechat", authDefaults.WeChat},
		{"github", authDefaults.GitHub},
		{"google", authDefaults.Google},
		{"dingtalk", authDefaults.DingTalk},
	} {
		for key, value := range map[string]any{
			"balance": provider.value.Balance, "concurrency": provider.value.Concurrency, "subscriptions": provider.value.Subscriptions,
			"grant_on_signup": provider.value.GrantOnSignup, "grant_on_first_bind": provider.value.GrantOnFirstBind,
		} {
			name := "auth_source_default_" + provider.name + "_" + key
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			require.JSONEq(t, string(encoded), string(data[name]), name)
			delete(data, name)
		}
	}
	require.JSONEq(t, "false", string(data["force_email_on_third_party_signup"]))
	delete(data, "force_email_on_third_party_signup")
	require.Empty(t, data)
}

// GetAllSettings records each read and returns the shared snapshot at or after
// responseRead. Earlier reads delegate to Runtime; calls is required, and the
// threshold counts attempted invocations even when delegation fails.
func (s *settingsResponseSource) GetAllSettings(ctx context.Context) (*composite.Snapshot, error) {
	*s.calls = append(*s.calls, "settings")
	s.reads++
	if s.reads >= s.responseRead {
		return s.snapshot, nil
	}
	return s.Runtime.GetAllSettings(ctx)
}

// GetAuthSourceDefaultSettings records its call before delegating to the
// embedded settings runtime.
func (s *settingsResponseSource) GetAuthSourceDefaultSettings(ctx context.Context) (*identity.AuthSourceDefaultSettings, error) {
	*s.calls = append(*s.calls, "auth")
	return s.Runtime.GetAuthSourceDefaultSettings(ctx)
}

// IsTotpEncryptionKeyConfigured records response assembly and returns the fixed
// configured-key marker.
func (s *settingsResponseSource) IsTotpEncryptionKeyConfigured() bool {
	*s.calls = append(*s.calls, "totp")
	return true
}

// GetOpenAIFastPolicySettings records the read and returns the configured
// shared policy pointer. Nil policy remains nil, letting response tests check
// optional omission rather than constructing another policy snapshot.
func (s *settingsResponseSource) GetOpenAIFastPolicySettings(context.Context) (*tierpolicy.OpenAIFastPolicySettings, error) {
	*s.calls = append(*s.calls, "fast")
	return s.fastPolicy, nil
}

// GetPaymentConfig records the payment read and returns config with err
// unchanged. Both may be nonnil, allowing GET and PUT response assembly to
// exercise their different payment failure handling.
func (s *settingsResponsePayment) GetPaymentConfig(context.Context) (*paymentcore.PaymentConfig, error) {
	*s.calls = append(*s.calls, "payment")
	return s.config, s.err
}

// IsMonitoringEnabled records the monitoring read and returns false for the
// fixture.
func (s *settingsResponseMonitoring) IsMonitoringEnabled(context.Context) bool {
	*s.calls = append(*s.calls, "monitoring")
	return false
}

// SetMonitoringEnabled accepts the fixture callback without changing its
// reported false value.
func (s *settingsResponseMonitoring) SetMonitoringEnabled(bool) {}

// newCompositeSettingsHTTPFixture constructs combined HTTP options with shared
// settings and grant readers. cfg is required, plan validation is a no-op, and
// the application callbacks publish forwarded-IP values then notify site.
// Payment channel refresh is disabled in the participant used by these
// scenarios.
func newCompositeSettingsHTTPFixture(repo settingscore.Repository, cfg *config.Config) (settingshttp.HandlerOptions, *settingscore.Store) {
	store := settingscore.New(repo)
	oauth := provideOAuthSettings(store, cfg)
	grants := identity.NewGrantSettings(store, identity.GrantSettingsOptions{DefaultBalance: cfg.UserDefaults.Balance, DefaultConcurrency: cfg.UserDefaults.Concurrency})
	gatewayRuntime := provideGatewaySettings(store)
	rules := provideGatewayAdminRules()
	defaults := provideSchedulerAdminDefaults(cfg)
	forwarded := provideForwardedSettings(store, cfg)
	read := composite.ReadOptions{
		OAuth: oauth, Gateway: *rules, Scheduler: *defaults,
		DefaultBalance:     func() float64 { return cfg.UserDefaults.Balance },
		DefaultConcurrency: func() int { return cfg.UserDefaults.Concurrency },
		Forwarded: func() runtimeconfig.ForwardedInput {
			value := cfg.ForwardedClientIPSettings()
			return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.TrustForwardedIP, ForwardedClientIPHeaders: value.Headers}
		},
	}
	prepare := composite.PrepareOptions{
		ReadValues: store.GetAll, Gateway: *rules, Scheduler: *defaults,
		ValidatePlans: func(context.Context, []billing.DefaultSubscriptionSetting) error { return nil },
	}
	applications := []composite.Application{
		{Module: "server", Apply: func(_ context.Context, value *composite.Snapshot) error {
			forwarded.Apply(runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.APIKeyACLTrustForwardedIP, ForwardedClientIPHeaders: value.ForwardedClientIPHeaders})
			return nil
		}},
		{Module: "site", Apply: func(context.Context, *composite.Snapshot) error { store.NotifyUpdated(); return nil }},
	}
	source := composite.NewRuntime(store, read, prepare, grants, gatewayRuntime, cfg.Security.SecretEncryptionKeyConfigured, applications)
	participants := staticSettingsParticipants(&paymentcore.Runtime{}, grants, defaults, rules)
	// Retain payment field preparation while disabling provider refresh for the
	// fixture, so combined persistence and response tests do not need configured payment-channel instances.
	participants[len(participants)-1] = paymentcore.SettingsParticipant(nil)
	registry, err := settingscore.NewRegistry(participants...)
	return settingshttp.HandlerOptions{Settings: source, Participants: registry, ParticipantError: err}, store
}

// TestSettingsFieldOwnership enumerates static participants and the flattened
// update request type. It checks unique ownership, all 287 request fields are
// represented, and no participant declares an extra field outside that request.
func TestSettingsFieldOwnership(t *testing.T) {
	defaults := scheduler.DefaultAdminSettingsDefaults()
	participants := staticSettingsParticipants(&paymentcore.Runtime{}, nil, &defaults, provideGatewayAdminRules())
	_, err := settingscore.NewRegistry(participants...)
	require.NoError(t, err)
	owners := map[string]string{}
	for _, participant := range participants {
		encoded, err := json.Marshal(struct {
			// Module identifies the participant while Fields and Keys expose its
			// declared ownership mapping. This JSON log supports field-registration inspection alongside the uniqueness assertions below.
			Module       string   // settings participant name
			Fields, Keys []string // Fields: flattened request names; Keys: persisted setting identities
		}{participant.Module, participant.Fields, participant.Keys})
		require.NoError(t, err)
		t.Logf("TEST_PARTICIPANT %s", encoded)
		for _, field := range participant.Fields {
			require.Empty(t, owners[field], "字段 %s 有多个所有者", field)
			owners[field] = participant.Module
		}
	}
	input := reflect.TypeFor[settingsdto.UpdateSettingsRequest]()
	count := 0
	for field := range input.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		require.Contains(t, owners, name, "输入字段 %s 没有静态所有者", field.Name)
		delete(owners, name)
		count++
	}
	require.Equal(t, 288, count, "HTTP 字段变化需要同步字段登记")
	require.Empty(t, owners, "参与者不应声明不存在的输入字段")
}

// TestSettingsRejectedFastPolicyHasNoWrites combines a site-name change with
// invalid Fast policy. It checks BadRequest and that the site name remains its
// previously stored value after the rejected patch.
func TestSettingsRejectedFastPolicyHasNoWrites(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{site.SettingKeySiteName: "before"})
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "after", "openai_fast_policy_settings": map[string]any{"rules": []map[string]any{{"service_tier": "priority", "action": "bogus", "scope": "all"}}}}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "before", repo.values[site.SettingKeySiteName], "后段校验拒绝后不应保存站点名称")
}

// SetMultiple counts the attempt before optionally returning an injected write
// error. Successful attempts delegate the batch to the embedded fixture; writes
// therefore describes attempts rather than committed transactions.
func (r *settingAtomicRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	r.writes++
	if r.failWrite {
		return errors.New("settings write failed")
	}
	return r.settingHandlerRepoStub.SetMultiple(ctx, values)
}

// GetAll injects a reload error after a write attempt when failApply is
// enabled. Otherwise it delegates the snapshot read, allowing scenarios to
// distinguish write rejection from saved-but-unapplied settings.
func (r *settingAtomicRepo) GetAll(ctx context.Context) (map[string]string, error) {
	if r.failApply && r.writes > 0 {
		return nil, errors.New("settings reload failed")
	}
	return r.settingHandlerRepoStub.GetAll(ctx)
}

// TestSettingsCombinedUpdateCommitsOnce updates site, payment, and Fast policy
// through combined HTTP. It checks successful response, one fixture batch
// write, and all three stored values from that accepted update.
func TestSettingsCombinedUpdateCommitsOnce(t *testing.T) {
	repo := &settingAtomicRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{site.SettingKeySiteName: "before"}}}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	payment := paymentcore.NewConfigService(nil, repo, nil, nil, paymentcore.ConfigurationRuntime{})
	options.Payment = payment
	h := settingshttp.NewHandler(options)
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "after", "payment_enabled": true, "openai_fast_policy_settings": map[string]any{"rules": []any{}}}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, repo.writes)
	require.Equal(t, "after", repo.values[site.SettingKeySiteName])
	require.Equal(t, "true", repo.values[paymentcore.SettingPaymentEnabled])
	require.Equal(t, `{"rules":[]}`, repo.values[gateway.SettingKeyOpenAIFastPolicySettings])
}

// TestSettingsCombinedWriteAndApplyErrors injects failure before the batch
// write or during post-write reload. It checks unsuccessful HTTP and no
// notification, distinguishing unchanged persistence from saved data with
// SETTINGS_APPLY_FAILED and persisted metadata.
func TestSettingsCombinedWriteAndApplyErrors(t *testing.T) {
	for _, scenario := range []string{"write", "apply"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &settingAtomicRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{site.SettingKeySiteName: "before"}}, failWrite: scenario == "write", failApply: scenario == "apply"}
			options, store := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
			h := settingshttp.NewHandler(options)
			notified := false
			store.Subscribe(func() { notified = true })
			rec := doUpdateSettings(t, h, map[string]any{"site_name": "after"}, nil)
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			require.False(t, notified)
			if scenario == "write" {
				require.Equal(t, "before", repo.values[site.SettingKeySiteName])
				return
			}
			require.Equal(t, "after", repo.values[site.SettingKeySiteName])
			require.Contains(t, rec.Body.String(), "SETTINGS_APPLY_FAILED")
			require.Contains(t, rec.Body.String(), "persisted")
		})
	}
}

// Get rejects unsupported single-record access in the failing batch fixture.
func (s *failingAuthSourceSettingsRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue rejects single-key access outside the fixture's batch settings path.
func (s *failingAuthSourceSettingsRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

// Set rejects individual writes in the all-or-rejected batch fixture.
func (s *failingAuthSourceSettingsRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple returns a fresh map of requested present fixture settings.
func (s *failingAuthSourceSettingsRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple returns the configured err unchanged when email-source balance is
// present, applying no keys. That includes nil err; other batches initialize
// values as needed and write each supplied string synchronously.
func (s *failingAuthSourceSettingsRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	if _, ok := settings[identity.SettingKeyAuthSourceDefaultEmailBalance]; ok {
		return s.err
	}
	for key, value := range settings {
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}

// GetAll returns a new outer map of the configured setting strings.
func (s *failingAuthSourceSettingsRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	maps.Copy(out, s.values)
	return out, nil
}

// Delete rejects removal from the failing settings write fixture.
func (s *failingAuthSourceSettingsRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// TestSettingHandler_GetSettings_InjectsAuthSourceDefaults reads stored
// email-source grants and force-email policy through combined settings HTTP. It
// checks balance, concurrency, subscription count, and the force-email flag in
// the flattened response.
func TestSettingHandler_GetSettings_InjectsAuthSourceDefaults(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "true",
			promotion.SettingKeyPromoCodeEnabled:                   "true",
			identity.SettingKeyAuthSourceDefaultEmailBalance:       "9.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "8",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":31}]`,
			identity.SettingKeyForceEmailOnThirdPartySignup:        "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)

	handler.GetSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, 9.5, data["auth_source_default_email_balance"])
	require.Equal(t, float64(8), data["auth_source_default_email_concurrency"])
	require.Equal(t, true, data["force_email_on_third_party_signup"])

	subscriptions, ok := data["auth_source_default_email_subscriptions"].([]any)
	require.True(t, ok)
	require.Len(t, subscriptions, 1)
}

// TestSettingHandler_UpdateSettings_PreservesOmittedAuthSourceDefaults updates
// email-source balance while omitting other grant defaults. It checks formatted
// stored balance, retained concurrency/subscriptions/force-email, and the
// corresponding response values after the partial save.
func TestSettingHandler_UpdateSettings_PreservesOmittedAuthSourceDefaults(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                    "false",
			promotion.SettingKeyPromoCodeEnabled:                      "true",
			identity.SettingKeyAuthSourceDefaultEmailBalance:          "9.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:      "8",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions:    `[{"plan_id":31}]`,
			identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup:    "true",
			identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind: "false",
			identity.SettingKeyForceEmailOnThirdPartySignup:           "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"registration_enabled":              true,
		"promo_code_enabled":                true,
		"auth_source_default_email_balance": 12.75,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "12.75000000", repo.values[identity.SettingKeyAuthSourceDefaultEmailBalance])
	require.Equal(t, "8", repo.values[identity.SettingKeyAuthSourceDefaultEmailConcurrency])
	require.Equal(t, `[{"plan_id":31}]`, repo.values[identity.SettingKeyAuthSourceDefaultEmailSubscriptions])
	require.Equal(t, "true", repo.values[identity.SettingKeyForceEmailOnThirdPartySignup])

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, 12.75, data["auth_source_default_email_balance"])
	require.Equal(t, float64(8), data["auth_source_default_email_concurrency"])
	require.Equal(t, true, data["force_email_on_third_party_signup"])
}

// TestSettingHandler_UpdateSettings_AcceptsMarkdownCustomMenuURL submits
// trimmed markdown menu URLs with slug, compact browser ID, and hyphenated ID
// forms. It checks accepted normalization and stored menu data, or rejection
// with no menu key written.
func TestSettingHandler_UpdateSettings_AcceptsMarkdownCustomMenuURL(t *testing.T) {
	for _, test := range []struct {
		id     string // requested custom menu identity
		status int    // expected validation status
	}{
		{"guide", http.StatusOK},
		{"00112233445566778899aabbccddeeff", http.StatusOK},
		{"00112233-4455-6677-8899-aabbccddeeff", http.StatusBadRequest},
	} {
		t.Run(test.id, func(t *testing.T) {
			repo := &settingHandlerRepoStub{values: map[string]string{promotion.SettingKeyPromoCodeEnabled: "true"}}
			options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
			handler := settingshttp.NewHandler(options)
			body := map[string]any{
				"promo_code_enabled": true,
				"custom_menu_items": []map[string]any{{
					"id": test.id, "label": "Guide", "icon_svg": "", "url": " md:guide ", "visibility": "user", "sort_order": 0,
				}},
			}
			rawBody, err := json.Marshal(body)
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
			c.Request.Header.Set("Content-Type", "application/json")
			handler.UpdateSettings(c)
			require.Equal(t, test.status, rec.Code, rec.Body.String())
			if test.status != http.StatusOK {
				require.NotContains(t, repo.values, site.SettingKeyCustomMenuItems)
				return
			}
			var saved []site.CustomMenuItem
			require.NoError(t, json.Unmarshal([]byte(repo.values[site.SettingKeyCustomMenuItems]), &saved))
			require.Equal(t, []site.CustomMenuItem{{ID: test.id, Label: "Guide", URL: "md:guide", Visibility: "user"}}, saved)
		})
	}
}

// TestSettingHandler_UpdateSettings_RejectsEmptyMarkdownCustomMenuSlug submits
// a markdown menu URL without its slug. It checks BadRequest and absence of the
// custom-menu storage key, keeping the invalid choice out of saved
// configuration.
func TestSettingHandler_UpdateSettings_RejectsEmptyMarkdownCustomMenuSlug(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled": true,
		"custom_menu_items": []map[string]any{
			{
				"id":         "guide",
				"label":      "Guide",
				"icon_svg":   "",
				"url":        "md:",
				"visibility": "user",
				"sort_order": 0,
			},
		},
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, repo.values, site.SettingKeyCustomMenuItems)
}

// TestSettingHandler_UpdateSettings_PersistsPaymentVisibleMethodsAndAdvancedScheduler
// updates visible channels, subscription priority, and quota pause thresholds
// over existing Ops JSON. It checks canonical storage and response values,
// retained retention fields, and removed aggregation data.
func TestSettingHandler_UpdateSettings_PersistsPaymentVisibleMethodsAndAdvancedScheduler(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
			ops.SettingKeyOpsAdvancedSettings:    `{"data_retention":{"cleanup_enabled":true,"cleanup_schedule":"0 4 * * *","error_log_retention_days":12,"minute_metrics_retention_days":8,"hourly_metrics_retention_days":30},"aggregation":{"aggregation_enabled":true},"openai_provider_quota_auto_pause":{"default_threshold_5h":0.6,"default_threshold_7d":0.7},"ignore_count_tokens_errors":true,"ignore_context_canceled":true,"ignore_no_available_providers":false,"ignore_invalid_api_key_errors":false,"ignore_insufficient_balance_errors":true,"display_openai_token_stats":false,"display_alert_events":true,"auto_refresh_enabled":false,"auto_refresh_interval_seconds":30}`,
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	paymentConfigService := paymentcore.NewConfigService(nil, repo, nil, nil, paymentcore.ConfigurationRuntime{})
	options.Payment = paymentConfigService
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":                               true,
		"payment_visible_method_alipay_source":             "easypay",
		"payment_visible_method_wxpay_source":              "wxpay",
		"payment_visible_method_alipay_enabled":            true,
		"payment_visible_method_wxpay_enabled":             false,
		"advanced_scheduler_subscription_priority_enabled": true,
		"openai_provider_quota_auto_pause": map[string]any{
			"default_threshold_5h": 0.95,
			"default_threshold_7d": 0.9,
		},
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, paymentcore.VisibleMethodSourceEasyPayAlipay, repo.values[paymentcore.SettingPaymentVisibleMethodAlipaySource])
	require.Equal(t, paymentcore.VisibleMethodSourceOfficialWechat, repo.values[paymentcore.SettingPaymentVisibleMethodWxpaySource])
	require.Equal(t, "true", repo.values[paymentcore.SettingPaymentVisibleMethodAlipayEnabled])
	require.Equal(t, "false", repo.values[paymentcore.SettingPaymentVisibleMethodWxpayEnabled])
	require.Equal(t, "true", repo.values[scheduler.SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled])
	var advanced ops.OpsAdvancedSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[ops.SettingKeyOpsAdvancedSettings]), &advanced))
	require.True(t, advanced.DataRetention.CleanupEnabled)
	require.Equal(t, "0 4 * * *", advanced.DataRetention.CleanupSchedule)
	require.NotContains(t, repo.values[ops.SettingKeyOpsAdvancedSettings], `"aggregation"`)
	require.Equal(t, 0.95, advanced.OpenAIProviderQuotaAutoPause.DefaultThreshold5h)
	require.Equal(t, 0.9, advanced.OpenAIProviderQuotaAutoPause.DefaultThreshold7d)

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, paymentcore.VisibleMethodSourceEasyPayAlipay, data["payment_visible_method_alipay_source"])
	require.Equal(t, paymentcore.VisibleMethodSourceOfficialWechat, data["payment_visible_method_wxpay_source"])
	require.Equal(t, true, data["payment_visible_method_alipay_enabled"])
	require.Equal(t, false, data["payment_visible_method_wxpay_enabled"])
	require.Equal(t, true, data["advanced_scheduler_subscription_priority_enabled"])
	quota, ok := data["openai_provider_quota_auto_pause"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0.95, quota["default_threshold_5h"])
	require.Equal(t, 0.9, quota["default_threshold_7d"])
}

// TestSettingHandler_UpdateSettings_PreservesLegacyBlankPaymentVisibleMethodSource
// updates an unrelated promo flag while payment source text is blank. It checks
// successful HTTP retains the blank Alipay source and its enabled stored flag.
func TestSettingHandler_UpdateSettings_PreservesLegacyBlankPaymentVisibleMethodSource(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled:                 "true",
			paymentcore.SettingPaymentVisibleMethodAlipayEnabled: "true",
			paymentcore.SettingPaymentVisibleMethodAlipaySource:  "",
			paymentcore.SettingPaymentVisibleMethodWxpayEnabled:  "false",
			paymentcore.SettingPaymentVisibleMethodWxpaySource:   "",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled": false,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "", repo.values[paymentcore.SettingPaymentVisibleMethodAlipaySource])
	require.Equal(t, "true", repo.values[paymentcore.SettingPaymentVisibleMethodAlipayEnabled])
}

// TestSettingHandler_UpdateSettings_PersistsExplicitFalseOIDCCompatibilityFlags
// sends explicit false OIDC PKCE and ID-token-validation values. It checks
// successful storage of both false strings and matching response booleans
// rather than applying their missing-value defaults.
func TestSettingHandler_UpdateSettings_PersistsExplicitFalseOIDCCompatibilityFlags(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled:              "true",
			identity.SettingKeyOIDCConnectEnabled:             "true",
			identity.SettingKeyOIDCConnectProviderName:        "OIDC",
			identity.SettingKeyOIDCConnectClientID:            "oidc-client",
			identity.SettingKeyOIDCConnectClientSecret:        "oidc-secret",
			identity.SettingKeyOIDCConnectIssuerURL:           "https://issuer.example.com",
			identity.SettingKeyOIDCConnectAuthorizeURL:        "https://issuer.example.com/auth",
			identity.SettingKeyOIDCConnectTokenURL:            "https://issuer.example.com/token",
			identity.SettingKeyOIDCConnectUserInfoURL:         "https://issuer.example.com/userinfo",
			identity.SettingKeyOIDCConnectJWKSURL:             "https://issuer.example.com/jwks",
			identity.SettingKeyOIDCConnectScopes:              "openid email profile",
			identity.SettingKeyOIDCConnectRedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
			identity.SettingKeyOIDCConnectFrontendRedirectURL: "/auth/oidc/callback",
			identity.SettingKeyOIDCConnectTokenAuthMethod:     "client_secret_post",
			identity.SettingKeyOIDCConnectUsePKCE:             "true",
			identity.SettingKeyOIDCConnectValidateIDToken:     "true",
			identity.SettingKeyOIDCConnectAllowedSigningAlgs:  "RS256",
			identity.SettingKeyOIDCConnectClockSkewSeconds:    "120",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":                true,
		"oidc_connect_enabled":              true,
		"oidc_connect_use_pkce":             false,
		"oidc_connect_validate_id_token":    false,
		"oidc_connect_allowed_signing_algs": "",
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectUsePKCE])
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectValidateIDToken])

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, data["oidc_connect_use_pkce"])
	require.Equal(t, false, data["oidc_connect_validate_id_token"])
}

// TestSettingHandler_UpdateSettings_DoesNotSolidifyImplicitOIDCSecurityDefaultsOnLegacyUpgrade
// updates enabled OIDC with stored security flags absent and implicit
// configuration defaults. It checks the compatibility path saves false values
// for PKCE and ID-token validation.
func TestSettingHandler_UpdateSettings_DoesNotSolidifyImplicitOIDCSecurityDefaultsOnLegacyUpgrade(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled:               "true",
			identity.SettingKeyOIDCConnectEnabled:              "true",
			identity.SettingKeyOIDCConnectProviderName:         "OIDC",
			identity.SettingKeyOIDCConnectClientID:             "oidc-client",
			identity.SettingKeyOIDCConnectClientSecret:         "oidc-secret",
			identity.SettingKeyOIDCConnectIssuerURL:            "https://issuer.example.com",
			identity.SettingKeyOIDCConnectAuthorizeURL:         "https://issuer.example.com/auth",
			identity.SettingKeyOIDCConnectTokenURL:             "https://issuer.example.com/token",
			identity.SettingKeyOIDCConnectUserInfoURL:          "https://issuer.example.com/userinfo",
			identity.SettingKeyOIDCConnectJWKSURL:              "https://issuer.example.com/jwks",
			identity.SettingKeyOIDCConnectScopes:               "openid email profile",
			identity.SettingKeyOIDCConnectRedirectURL:          "https://example.com/api/v1/auth/oauth/oidc/callback",
			identity.SettingKeyOIDCConnectFrontendRedirectURL:  "/auth/oidc/callback",
			identity.SettingKeyOIDCConnectTokenAuthMethod:      "client_secret_post",
			identity.SettingKeyOIDCConnectAllowedSigningAlgs:   "RS256",
			identity.SettingKeyOIDCConnectClockSkewSeconds:     "120",
			identity.SettingKeyOIDCConnectRequireEmailVerified: "false",
			identity.SettingKeyOIDCConnectUserInfoEmailPath:    "",
			identity.SettingKeyOIDCConnectUserInfoIDPath:       "",
			identity.SettingKeyOIDCConnectUserInfoUsernamePath: "",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{
		UserDefaults: config.UserDefaultsConfig{Concurrency: 5},
		Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
			Enabled:             true,
			ProviderName:        "OIDC",
			ClientID:            "oidc-client",
			ClientSecret:        "oidc-secret",
			IssuerURL:           "https://issuer.example.com",
			AuthorizeURL:        "https://issuer.example.com/auth",
			TokenURL:            "https://issuer.example.com/token",
			UserInfoURL:         "https://issuer.example.com/userinfo",
			JWKSURL:             "https://issuer.example.com/jwks",
			Scopes:              "openid email profile",
			RedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
			FrontendRedirectURL: "/auth/oidc/callback",
			TokenAuthMethod:     "client_secret_post",
			UsePKCE:             true,
			ValidateIDToken:     true,
			AllowedSigningAlgs:  "RS256",
			ClockSkewSeconds:    120,
		}}},
	})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":   true,
		"oidc_connect_enabled": true,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectUsePKCE])
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectValidateIDToken])
}

// TestSettingHandler_UpdateSettings_RejectsInvalidPaymentVisibleMethodSource
// supplies an unsupported Alipay source while changing the promo flag. It
// checks BadRequest and no source key is written, keeping invalid channel
// choices out of persistence.
func TestSettingHandler_UpdateSettings_RejectsInvalidPaymentVisibleMethodSource(t *testing.T) {
	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":                   true,
		"payment_visible_method_alipay_source": "bogus",
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, repo.values, paymentcore.SettingPaymentVisibleMethodAlipaySource)
}

// TestSettingHandler_UpdateSettings_DoesNotPersistPartialSystemSettingsWhenAuthSourceDefaultsFail
// injects failure for a batch containing email-source balance and system
// settings. It checks InternalServerError and retains both the old registration
// flag and the prior balance.
func TestSettingHandler_UpdateSettings_DoesNotPersistPartialSystemSettingsWhenAuthSourceDefaultsFail(t *testing.T) {
	repo := &failingAuthSourceSettingsRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "false",
			promotion.SettingKeyPromoCodeEnabled:                   "true",
			identity.SettingKeyAuthSourceDefaultEmailBalance:       "9.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "8",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":31}]`,
		},
		err: errors.New("write auth source defaults failed"),
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"registration_enabled":              true,
		"promo_code_enabled":                true,
		"auth_source_default_email_balance": 12.75,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyRegistrationEnabled])
	require.Equal(t, "9.5", repo.values[identity.SettingKeyAuthSourceDefaultEmailBalance])
}

// TestDiffSettings_IncludesAuthSourceDefaultsAndForceEmail compares changed
// email grants and force-email policy alongside unchanged system snapshots. It
// checks each balance, concurrency, subscription, signup/first-bind, and
// force-email identity appears in the diff.
func TestDiffSettings_IncludesAuthSourceDefaultsAndForceEmail(t *testing.T) {
	changed := settingshttp.DiffSettings(
		&composite.Snapshot{},
		&composite.Snapshot{},
		&identity.AuthSourceDefaultSettings{
			Email: identity.ProviderDefaultGrantSettings{
				Balance:          0,
				Concurrency:      5,
				Subscriptions:    nil,
				GrantOnSignup:    true,
				GrantOnFirstBind: false,
			},
			ForceEmailOnThirdPartySignup: false,
		},
		&identity.AuthSourceDefaultSettings{
			Email: identity.ProviderDefaultGrantSettings{
				Balance:          12.5,
				Concurrency:      7,
				Subscriptions:    []identity.DefaultSubscriptionSetting{{PlanID: 21}},
				GrantOnSignup:    false,
				GrantOnFirstBind: true,
			},
			ForceEmailOnThirdPartySignup: true,
		},
		settingsdto.UpdateSettingsRequest{},
	)

	require.Contains(t, changed, "auth_source_default_email_balance")
	require.Contains(t, changed, "auth_source_default_email_concurrency")
	require.Contains(t, changed, "auth_source_default_email_subscriptions")
	require.Contains(t, changed, "auth_source_default_email_grant_on_signup")
	require.Contains(t, changed, "auth_source_default_email_grant_on_first_bind")
	require.Contains(t, changed, "force_email_on_third_party_signup")
}

// newDingTalkSettingsHandler constructs combined HTTP with empty stored
// settings and fixture defaults.
func newDingTalkSettingsHandler() (*settingshttp.Handler, *settingHandlerRepoStub) {
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)
	return handler, repo
}

// baseValidDingTalkBody returns fresh complete DingTalk credentials with
// unrestricted corporate policy.
func baseValidDingTalkBody() map[string]any {
	return map[string]any{
		"dingtalk_connect_enabled":                 true,
		"dingtalk_connect_client_id":               "test-client-id",
		"dingtalk_connect_client_secret":           "test-client-secret",
		"dingtalk_connect_redirect_url":            "https://example.com/auth/dingtalk/callback",
		"dingtalk_connect_corp_restriction_policy": "none",
	}
}

// TestSettingsPUT_DingTalk_V3_InternalOnlyAllowsEmptyCorpID sends enabled
// internal-only DingTalk with empty internal corporate identity. It checks
// successful HTTP accepts that input, limiting the scenario to the observed
// validation/save response.
func TestSettingsPUT_DingTalk_V3_InternalOnlyAllowsEmptyCorpID(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "internal_only"
	body["dingtalk_connect_internal_corp_id"] = ""

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestSettingsPUT_DingTalk_HappyPath_None sends complete enabled DingTalk
// credentials with unrestricted corporate policy. It checks successful HTTP and
// the enabled flag in returned combined settings, covering the accepted
// configuration path.
func TestSettingsPUT_DingTalk_HappyPath_None(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "none"

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["dingtalk_connect_enabled"])
}

// TestSettingsPUT_DingTalk_HappyPath_InternalOnly_WithCorpID sends complete
// DingTalk credentials under internal-only policy with a corporate ID. It
// checks HTTP OK for that accepted combination of corporate policy and supplied
// identity.
func TestSettingsPUT_DingTalk_HappyPath_InternalOnly_WithCorpID(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "internal_only"
	body["dingtalk_connect_internal_corp_id"] = "ding-corp-123"

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestSettingsPUT_DingTalk_BypassRegistration_RoundTrip enables registration
// bypass under internal-only DingTalk policy. It checks successful HTTP returns
// the bypass flag true after the combined settings update has accepted that
// policy.
func TestSettingsPUT_DingTalk_BypassRegistration_RoundTrip(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "internal_only"
	body["dingtalk_connect_bypass_registration"] = true

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["dingtalk_connect_bypass_registration"])
}

// TestSettingsPUT_DingTalk_Disabled_SkipsValidation sends disabled DingTalk
// with an empty client ID and internal-only policy. It checks successful HTTP
// accepts the incomplete disabled-provider configuration in this settings
// scenario.
func TestSettingsPUT_DingTalk_Disabled_SkipsValidation(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := map[string]any{
		"dingtalk_connect_enabled":                 false,
		"dingtalk_connect_client_id":               "",
		"dingtalk_connect_corp_restriction_policy": "internal_only",
	}

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestSettingsPUT_DingTalk_SyncFlags_InternalOnly_RoundTrip enables all three
// profile sync switches under internal-only DingTalk policy. It checks
// successful HTTP returns email, display-name, and department sync flags as
// true.
func TestSettingsPUT_DingTalk_SyncFlags_InternalOnly_RoundTrip(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "internal_only"
	body["dingtalk_connect_sync_corp_email"] = true
	body["dingtalk_connect_sync_display_name"] = true
	body["dingtalk_connect_sync_dept"] = true

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["dingtalk_connect_sync_corp_email"], "sync_corp_email should be true for internal_only")
	require.Equal(t, true, data["dingtalk_connect_sync_display_name"], "sync_display_name should be true for internal_only")
	require.Equal(t, true, data["dingtalk_connect_sync_dept"], "sync_dept should be true for internal_only")
}

// TestSettingsPUT_DingTalk_SyncFlags_PolicyNone_CoercedToFalse sends enabled
// profile sync switches under unrestricted DingTalk policy. It checks
// successful HTTP returns all three switches false after the policy
// normalization path.
func TestSettingsPUT_DingTalk_SyncFlags_PolicyNone_CoercedToFalse(t *testing.T) {
	handler, _ := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "none"
	body["dingtalk_connect_sync_corp_email"] = true
	body["dingtalk_connect_sync_display_name"] = true
	body["dingtalk_connect_sync_dept"] = true

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, data["dingtalk_connect_sync_corp_email"], "sync_corp_email must be coerced to false when policy=none")
	require.Equal(t, false, data["dingtalk_connect_sync_display_name"], "sync_display_name must be coerced to false when policy=none")
	require.Equal(t, false, data["dingtalk_connect_sync_dept"], "sync_dept must be coerced to false when policy=none")
}

// TestSettingsPUT_DingTalk_StaleWhitelist_CoercedToNone submits the historical
// whitelist corporate policy text. It checks successful HTTP writes the
// canonical none policy into stored DingTalk settings, covering compatibility
// normalization before persistence.
func TestSettingsPUT_DingTalk_StaleWhitelist_CoercedToNone(t *testing.T) {
	handler, repo := newDingTalkSettingsHandler()

	body := baseValidDingTalkBody()
	body["dingtalk_connect_corp_restriction_policy"] = "whitelist"

	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "none", repo.values[identity.SettingKeyDingTalkConnectCorpRestrictionPolicy],
		"stale whitelist 应在写入路径被 coerce 为 none")
}

// TestSettingsPUT_DingTalk_SyncAttrKey_RoundTrip updates custom sync attribute
// keys and then explicitly empty keys. It checks custom keys in
// storage/response and the canonical email, name, and department defaults
// stored for empty inputs.
func TestSettingsPUT_DingTalk_SyncAttrKey_RoundTrip(t *testing.T) {
	t.Run("custom_attr_keys_saved", func(t *testing.T) {
		handler, repo := newDingTalkSettingsHandler()

		body := baseValidDingTalkBody()
		body["dingtalk_connect_corp_restriction_policy"] = "internal_only"
		body["dingtalk_connect_sync_corp_email"] = true
		body["dingtalk_connect_sync_display_name"] = true
		body["dingtalk_connect_sync_dept"] = true
		body["dingtalk_connect_sync_corp_email_attr_key"] = "my_email_attr"
		body["dingtalk_connect_sync_display_name_attr_key"] = "my_name_attr"
		body["dingtalk_connect_sync_dept_attr_key"] = "my_dept_attr"

		rawBody, err := json.Marshal(body)
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
		c.Request.Header.Set("Content-Type", "application/json")

		handler.UpdateSettings(c)

		require.Equal(t, http.StatusOK, rec.Code)

		require.Equal(t, "my_email_attr", repo.values[identity.SettingKeyDingTalkConnectSyncCorpEmailAttrKey])
		require.Equal(t, "my_name_attr", repo.values[identity.SettingKeyDingTalkConnectSyncDisplayNameAttrKey])
		require.Equal(t, "my_dept_attr", repo.values[identity.SettingKeyDingTalkConnectSyncDeptAttrKey])

		var resp response.Response
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		data, ok := resp.Data.(map[string]any)
		require.True(t, ok)
		require.Equal(t, "my_email_attr", data["dingtalk_connect_sync_corp_email_attr_key"])
		require.Equal(t, "my_name_attr", data["dingtalk_connect_sync_display_name_attr_key"])
		require.Equal(t, "my_dept_attr", data["dingtalk_connect_sync_dept_attr_key"])
	})

	t.Run("empty_attr_keys_fallback_to_defaults", func(t *testing.T) {
		handler, repo := newDingTalkSettingsHandler()

		body := baseValidDingTalkBody()
		body["dingtalk_connect_corp_restriction_policy"] = "internal_only"
		// Explicit empty attribute keys request the defaults.
		body["dingtalk_connect_sync_corp_email_attr_key"] = ""
		body["dingtalk_connect_sync_display_name_attr_key"] = ""
		body["dingtalk_connect_sync_dept_attr_key"] = ""

		rawBody, err := json.Marshal(body)
		require.NoError(t, err)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
		c.Request.Header.Set("Content-Type", "application/json")

		handler.UpdateSettings(c)

		require.Equal(t, http.StatusOK, rec.Code)

		// The resolved defaults are persisted.
		require.Equal(t, "dingtalk_email", repo.values[identity.SettingKeyDingTalkConnectSyncCorpEmailAttrKey])
		require.Equal(t, "dingtalk_name", repo.values[identity.SettingKeyDingTalkConnectSyncDisplayNameAttrKey])
		require.Equal(t, "dingtalk_department", repo.values[identity.SettingKeyDingTalkConnectSyncDeptAttrKey])
	})
}

// TestDiffSettingsTracksGoogleOneTapSwitch compares disabled and enabled Google
// One Tap snapshots. It checks the changed-field result includes the separate
// google_one_tap_enabled setting identity for the administrator diff.
func TestDiffSettingsTracksGoogleOneTapSwitch(t *testing.T) {
	before := &composite.Snapshot{GoogleOneTapEnabled: false}
	after := &composite.Snapshot{GoogleOneTapEnabled: true}

	changed := settingshttp.DiffSettings(before, after, nil, nil, settingsdto.UpdateSettingsRequest{})

	require.Contains(t, changed, "google_one_tap_enabled")
}

// TestUpdateSettingsNormalizedModuleFields exercises omitted, empty, and null
// values across scalar and pointer fields. It checks stored text and response
// defaults distinguish explicit clearing from retention according to each field
// representation.
func TestUpdateSettingsNormalizedModuleFields(t *testing.T) {
	tests := []struct {
		// field identifies the same stored key and response member. stored and
		// storedValue form the retained case; emptyValue, emptyStored, and optional emptyResponse define explicit clearing, while pointerField controls null retention.
		field         string // updated setting identity
		stored        string // initial persisted text
		storedValue   any    // initial interpreted value
		emptyValue    any    // explicit cleared input
		emptyResponse any    // cleared response override
		emptyStored   string // cleared persisted text
		pointerField  bool   // null retains stored input
	}{
		{field: "default_balance", stored: "12.00000000", storedValue: float64(12), emptyValue: float64(0), emptyStored: "0.00000000"},
		{field: "default_subscriptions", stored: `[{"plan_id":17}]`, storedValue: []any{map[string]any{"plan_id": float64(17)}}, emptyValue: []any{}, emptyStored: "[]"},
		{field: "balance_low_notify_enabled", stored: "true", storedValue: true, emptyValue: false, emptyStored: "false", pointerField: true},
		{field: "balance_low_notify_threshold", stored: "3.00000000", storedValue: float64(3), emptyValue: float64(0), emptyStored: "0.00000000", pointerField: true},
		{field: "enable_model_fallback", stored: "true", storedValue: true, emptyValue: false, emptyStored: "false"},
		{field: "fallback_model_openai", stored: "gpt-fallback", storedValue: "gpt-fallback", emptyValue: "", emptyResponse: "gpt-4o", emptyStored: ""},
		{field: "site_name", stored: "Example Gateway", storedValue: "Example Gateway", emptyValue: "", emptyResponse: "TokenRouter", emptyStored: ""},
		{field: "min_claude_code_version", stored: "1.2.3", storedValue: "1.2.3", emptyValue: "", emptyStored: ""},
		{field: "backend_mode_enabled", stored: "true", storedValue: true, emptyValue: false, emptyStored: "false"},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			emptyResponse := tt.emptyValue
			if tt.emptyResponse != nil {
				emptyResponse = tt.emptyResponse
			}
			for _, mode := range []string{"omitted", "empty", "null"} {
				t.Run(mode, func(t *testing.T) {
					h, repo := newStepUpSwitchTestHandler(t, map[string]string{tt.field: tt.stored})
					payload := map[string]any{"risk_control_enabled": true}
					wantStored, wantValue := tt.stored, tt.storedValue
					if mode == "empty" {
						payload[tt.field] = tt.emptyValue
						wantStored, wantValue = tt.emptyStored, emptyResponse
					}
					if mode == "null" {
						payload[tt.field] = nil
						if !tt.pointerField {
							wantStored, wantValue = tt.emptyStored, emptyResponse
						}
					}
					rec := doUpdateSettings(t, h, payload, nil)
					require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					require.Equal(t, wantStored, repo.values[tt.field])
					var response struct {
						Data map[string]any `json:"data"` // decoded combined response values
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
					require.Equal(t, wantValue, response.Data[tt.field])
				})
			}
		})
	}
}

// TestUpdateSettingsPartialPayloadKeepsUnsentKeys changes risk control while
// site, SMTP, and Turnstile values are stored. It checks the sent flag is
// written and each unsent value remains available after successful HTTP.
func TestUpdateSettingsPartialPayloadKeepsUnsentKeys(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		site.SettingKeySiteName:             "Example Gateway",
		site.SettingKeySiteSubtitle:         "Example Gateway Platform",
		notification.SettingKeySMTPHost:     "smtp.example.com",
		notification.SettingKeySMTPFrom:     "noreply@example.com",
		identity.SettingKeyTurnstileEnabled: "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"risk_control_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "true", repo.values[moderation.SettingKeyRiskControlEnabled],
		"调用方明确发送的字段必须写入")

	require.Equal(t, "Example Gateway", repo.values[site.SettingKeySiteName])
	require.Equal(t, "Example Gateway Platform", repo.values[site.SettingKeySiteSubtitle])
	require.Equal(t, "smtp.example.com", repo.values[notification.SettingKeySMTPHost])
	require.Equal(t, "noreply@example.com", repo.values[notification.SettingKeySMTPFrom])
	require.Equal(t, "true", repo.values[identity.SettingKeyTurnstileEnabled])
}

// TestUpdateSettingsFullPayloadStillClearsSentEmptyFields sends an empty
// site-name string over an existing value. It checks successful HTTP stores the
// empty value, preserving the distinction between explicit clearing and an
// omitted field.
func TestUpdateSettingsFullPayloadStillClearsSentEmptyFields(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		site.SettingKeySiteName: "Example Gateway",
	})

	rec := doUpdateSettings(t, h, map[string]any{"site_name": ""}, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "", repo.values[site.SettingKeySiteName],
		"明确发送的空值表示主动清空，而不是省略字段")
}

// TestUpdateSettingsSMTPFromAliasIsWritable sends the smtp_from_email request
// alias over a stored sender. It checks successful HTTP updates the canonical
// SMTP sender storage key with the supplied address.
func TestUpdateSettingsSMTPFromAliasIsWritable(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		notification.SettingKeySMTPFrom: "old@example.com",
	})

	rec := doUpdateSettings(t, h, map[string]any{"smtp_from_email": "new@example.com"}, nil)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "new@example.com", repo.values[notification.SettingKeySMTPFrom])
}

// TestUpdateSettingsCreativeEnabledPartialSemantics explicitly disables
// creative and then updates another flag with creative omitted. It checks false
// is persisted when sent and an already disabled creative value remains stored
// when omitted.
func TestUpdateSettingsCreativeEnabledPartialSemantics(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		creative.SettingKeyCreativeEnabled: "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"creative_enabled": false}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[creative.SettingKeyCreativeEnabled])

	h2, repo2 := newStepUpSwitchTestHandler(t, map[string]string{
		creative.SettingKeyCreativeEnabled: "false",
	})
	rec = doUpdateSettings(t, h2, map[string]any{"risk_control_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo2.values[creative.SettingKeyCreativeEnabled],
		"未发送 creative_enabled 时必须保留存储值")
}

// TestUpdateSettingsCreativeModelSettingsPartialSemantics omits, clears, and
// submits an invalid creative model operation list. It checks omission retains
// JSON, an empty array saves no models, and rejected capability input leaves
// that saved array unchanged.
func TestUpdateSettingsCreativeModelSettingsPartialSemantics(t *testing.T) {
	stored := `[{"group_id":12,"model":"gpt-image-2","operations":["generate"]}]`
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		creative.SettingKeyCreativeModelSettings: stored,
	})

	// Omitting the allowlist retains the stored model settings.
	rec := doUpdateSettings(t, h, map[string]any{"risk_control_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, stored, repo.values[creative.SettingKeyCreativeModelSettings])

	// An empty array disables all creative image models.
	rec = doUpdateSettings(t, h, map[string]any{"creative_model_settings": []any{}}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, "[]", repo.values[creative.SettingKeyCreativeModelSettings])

	// Invalid capabilities are rejected before the stored settings are replaced.
	rec = doUpdateSettings(t, h, map[string]any{
		"creative_model_settings": []map[string]any{{
			"group_id":   12,
			"model":      "gpt-image-2",
			"operations": []string{"upscale"},
		}},
	}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, "[]", repo.values[creative.SettingKeyCreativeModelSettings])
}

// TestUpdateSettingsCreativeWorkerCountPartialSemantics changes creative worker
// count and then submits an unrelated partial update. It checks the new count
// is stored and the omitted worker setting remains unchanged afterward.
func TestUpdateSettingsCreativeWorkerCountPartialSemantics(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		creative.SettingKeyCreativeWorkerCount: "4",
	})

	rec := doUpdateSettings(t, h, map[string]any{"creative_worker_count": 7}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "7", repo.values[creative.SettingKeyCreativeWorkerCount])

	rec = doUpdateSettings(t, h, map[string]any{"risk_control_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "7", repo.values[creative.SettingKeyCreativeWorkerCount])
}

// TestUpdateSettingsRejectsInvalidCreativeWorkerCount submits zero and negative
// counts over a configured creative worker value. It checks BadRequest for both
// inputs and retains the same stored positive count.
func TestUpdateSettingsRejectsInvalidCreativeWorkerCount(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		creative.SettingKeyCreativeWorkerCount: "4",
	})

	for _, value := range []int{0, -1} {
		rec := doUpdateSettings(t, h, map[string]any{"creative_worker_count": value}, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Equal(t, "4", repo.values[creative.SettingKeyCreativeWorkerCount])
	}
}

// TestUpdateSettingsNormalizesGeminiInpaintBeforeSave uses the fixture
// normalizer for group twelve with generate and inpaint. It checks successful
// HTTP stores only generation after the callback filters the unsupported
// fixture operation.
func TestUpdateSettingsNormalizesGeminiInpaintBeforeSave(t *testing.T) {
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	options.Creative = &creativeModelCandidateReaderStub{sanitizeGemini: true}
	h := settingshttp.NewHandler(options)

	rec := doUpdateSettings(t, h, map[string]any{
		"creative_model_settings": []map[string]any{{
			"group_id":   12,
			"model":      "gemini-3.1-flash-image",
			"operations": []string{"generate", "inpaint"},
		}},
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"group_id":12,"model":"gemini-3.1-flash-image","operations":["generate"]}]`, repo.values[creative.SettingKeyCreativeModelSettings])
}

// ListCreativeModelCandidates returns the fixture's shared candidate slice and
// nested operation lists. Callers keep those values stable during HTTP
// conversion or copy them before mutating them, since edits would change the
// fixture's supplied candidates.
func (s *creativeModelCandidateReaderStub) ListCreativeModelCandidates(context.Context) ([]creative.CreativeModelCandidate, error) {
	return s.candidates, nil
}

// NormalizeCreativeModelSettingsForSave returns input unchanged when
// sanitizeGemini is false. Otherwise it applies creative normalization and
// removes inpaint from every group-twelve entry, regardless of that entry's
// model name or platform.
func (s *creativeModelCandidateReaderStub) NormalizeCreativeModelSettingsForSave(_ context.Context, input []creative.CreativeModelSetting) ([]creative.CreativeModelSetting, error) {
	if !s.sanitizeGemini {
		return input, nil
	}
	normalized, err := creative.NormalizeCreativeModelSettings(input)
	if err != nil {
		return nil, err
	}
	for i := range normalized {
		if normalized[i].GroupID != 12 {
			continue
		}
		// The fixture filters group twelve regardless of model identity. Reuse
		// its normalized operation storage while retaining generation choices and removing each inpaint occurrence before save.
		filtered := normalized[i].Operations[:0]
		for _, operation := range normalized[i].Operations {
			if operation != creative.CreativeOperationInpaint {
				filtered = append(filtered, operation)
			}
		}
		normalized[i].Operations = filtered
	}
	return normalized, nil
}

// TestListCreativeModelCandidates serves one configured Grok image candidate
// through creative settings HTTP. It checks successful output includes the
// fixture group name and model identity from that candidate.
func TestListCreativeModelCandidates(t *testing.T) {
	h := creativehttp.NewSettingsHandler(&creativeModelCandidateReaderStub{candidates: []creative.CreativeModelCandidate{{
		GroupID:    12,
		GroupName:  "Exclusive Images",
		Platform:   "grok",
		Model:      "grok-imagine",
		Operations: []string{creative.CreativeOperationGenerate},
	}}}, nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/creative-model-candidates", nil)
	h.ListCreativeModelCandidates(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Exclusive Images")
	require.Contains(t, rec.Body.String(), "grok-imagine")
}

// TestGetCreativeWorkerStatus reads status from a nil worker method and a
// populated callback snapshot. It checks successful HTTP returns the respective
// running, worker-count, and busy-worker values for both sources.
func TestGetCreativeWorkerStatus(t *testing.T) {
	var worker *creative.CreativeWorkerRuntime
	h := creativehttp.NewSettingsHandler(nil, worker.Status)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/creative-worker-status", nil)
	h.GetCreativeWorkerStatus(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"running":false`)
	require.Contains(t, rec.Body.String(), `"worker_count":0`)
	require.Contains(t, rec.Body.String(), `"busy_workers":0`)

	h = creativehttp.NewSettingsHandler(nil, func() creative.CreativeWorkerStatus {
		return creative.CreativeWorkerStatus{Running: true, WorkerCount: 128, BusyWorkers: 60}
	})
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/creative-worker-status", nil)
	h.GetCreativeWorkerStatus(c)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"running":true`)
	require.Contains(t, rec.Body.String(), `"worker_count":128`)
	require.Contains(t, rec.Body.String(), `"busy_workers":60`)
}

// TestUpdateSettingsGrokDefaultBaseURLModeIsWritable updates a stored CLI mode
// to a regional Grok mode. It checks successful HTTP saves the requested
// eu-west-1 value in the canonical gateway setting key.
func TestUpdateSettingsGrokDefaultBaseURLModeIsWritable(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		gateway.SettingKeyGrokDefaultBaseURLMode: "cli",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"grok_default_base_url_mode": "eu-west-1",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "eu-west-1", repo.values[gateway.SettingKeyGrokDefaultBaseURLMode])
}

// TestUpdateSettingsRejectsTwoCaptchaProviders enables Turnstile and Tencent
// together with complete-looking credential input. It checks BadRequest and the
// mutually-exclusive-provider message, keeping that combination out of the
// accepted settings path.
func TestUpdateSettingsRejectsTwoCaptchaProviders(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyTurnstileEnabled:   "true",
		identity.SettingKeyTurnstileSiteKey:   "site-key",
		identity.SettingKeyTurnstileSecretKey: "turnstile-secret",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"turnstile_enabled":                true,
		"turnstile_site_key":               "site-key",
		"turnstile_secret_key":             "turnstile-secret",
		"tencent_captcha_enabled":          true,
		"tencent_captcha_app_id":           "123456789",
		"tencent_captcha_app_secret_key":   "app-secret",
		"tencent_captcha_cloud_secret_id":  "cloud-secret-id",
		"tencent_captcha_cloud_secret_key": "cloud-secret-key",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "cannot be enabled at the same time")
}

// TestUpdateSettingsRequiresFourTencentCaptchaCredentialsWhenEnabled enables
// Tencent with only an app ID supplied. It checks BadRequest and an
// AppSecretKey diagnostic, covering the missing credential rejection reached by
// that input.
func TestUpdateSettingsRequiresFourTencentCaptchaCredentialsWhenEnabled(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{
		"tencent_captcha_enabled": true,
		"tencent_captcha_app_id":  "123456789",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "AppSecretKey")
}

// TestUpdateSettingsRetainsStoredTencentCaptchaCredentialsWhenInputsEmpty
// enables Tencent while submitting blank sensitive fields over stored secrets.
// It checks successful HTTP retains all three configured secret values used to
// complete the accepted captcha options.
func TestUpdateSettingsRetainsStoredTencentCaptchaCredentialsWhenInputsEmpty(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyTencentCaptchaAppSecretKey:   "stored-app-secret",
		identity.SettingKeyTencentCaptchaCloudSecretID:  "stored-cloud-secret-id",
		identity.SettingKeyTencentCaptchaCloudSecretKey: "stored-cloud-secret-key",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"tencent_captcha_enabled":          true,
		"tencent_captcha_app_id":           "123456789",
		"tencent_captcha_app_secret_key":   "",
		"tencent_captcha_cloud_secret_id":  "",
		"tencent_captcha_cloud_secret_key": "",
	}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "stored-app-secret", repo.values[identity.SettingKeyTencentCaptchaAppSecretKey])
	require.Equal(t, "stored-cloud-secret-id", repo.values[identity.SettingKeyTencentCaptchaCloudSecretID])
	require.Equal(t, "stored-cloud-secret-key", repo.values[identity.SettingKeyTencentCaptchaCloudSecretKey])
}

// TestUpdateSettingsPartialPayloadKeepsTencentCaptchaRegion updates risk
// control while Tencent region is international. It checks successful HTTP
// retains the stored international choice when the captcha region is omitted.
func TestUpdateSettingsPartialPayloadKeepsTencentCaptchaRegion(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyTencentCaptchaRegion: identity.TencentCaptchaRegionINTL,
	})

	rec := doUpdateSettings(t, h, map[string]any{"risk_control_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, identity.TencentCaptchaRegionINTL,
		repo.values[identity.SettingKeyTencentCaptchaRegion])
}

// TestUpdateSettingsNormalizesUnknownTencentCaptchaRegion submits an
// unrecognized region over an international stored value. It checks successful
// HTTP writes the canonical China region rather than retaining an unsupported
// region string.
func TestUpdateSettingsNormalizesUnknownTencentCaptchaRegion(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyTencentCaptchaRegion: identity.TencentCaptchaRegionINTL,
	})

	rec := doUpdateSettings(t, h, map[string]any{"tencent_captcha_region": "sgp"}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, identity.TencentCaptchaRegionCN,
		repo.values[identity.SettingKeyTencentCaptchaRegion],
		"未知站点必须落回中国站，不能写入无法识别的值")
}

// TestUpdateSettingsWritesTencentCaptchaRegionWhenSent sends the recognized
// international captcha region in a partial payload. It checks successful HTTP
// writes that canonical value into its stored setting key.
func TestUpdateSettingsWritesTencentCaptchaRegionWhenSent(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"tencent_captcha_region": "intl"}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, identity.TencentCaptchaRegionINTL,
		repo.values[identity.SettingKeyTencentCaptchaRegion])
}

// TestUpdateSettingsValidatesTencentCaptchaAppIDWhenEnabledFlagIsOmitted keeps
// Tencent enabled in storage and submits a nonnumeric app ID. It checks
// BadRequest and positive-integer validation even though the update omits the
// enabled flag.
func TestUpdateSettingsValidatesTencentCaptchaAppIDWhenEnabledFlagIsOmitted(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyTencentCaptchaEnabled:        "true",
		identity.SettingKeyTencentCaptchaAppID:          "123456789",
		identity.SettingKeyTencentCaptchaAppSecretKey:   "stored-app-secret",
		identity.SettingKeyTencentCaptchaCloudSecretID:  "stored-cloud-secret-id",
		identity.SettingKeyTencentCaptchaCloudSecretKey: "stored-cloud-secret-key",
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"tencent_captcha_app_id": "not-a-number",
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "positive integer")
}

// newStepUpSwitchTestHandler constructs combined HTTP over the supplied stored
// map. It retains that map in the repository for mutation assertions and uses a
// default concurrency of five for settings preparation.
func newStepUpSwitchTestHandler(t *testing.T, stored map[string]string) (*settingshttp.Handler, *settingHandlerRepoStub) {
	t.Helper()

	repo := &settingHandlerRepoStub{values: stored}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	return settingshttp.NewHandler(options), repo
}

// TestUpdateSettingsEnableStepUpRejectsWithoutSession requests the
// disabled-to-enabled step-up transition without a session. It checks
// Forbidden, the TOTP prerequisite reason, and absence of a saved true step-up
// value.
func TestUpdateSettingsEnableStepUpRejectsWithoutSession(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": true}, nil)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_ENABLE_REQUIRES_TOTP")
	require.NotEqual(t, "true", repo.values[identity.SettingKeyStepUpEnabled])
}

// TestUpdateSettingsEnableStepUpRejectsAdminAPIKey requests step-up enablement
// using an administrator API-Key marker. It checks Forbidden with the dedicated
// admin-key rejection reason before the settings transition is accepted.
func TestUpdateSettingsEnableStepUpRejectsAdminAPIKey(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": true}, func(c *gin.Context) {
		c.Set("auth_method", audit.AuditAuthMethodAdminAPIKey)
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
}

// TestUpdateSettingsEnableStepUpFailsClosedWithoutUserService provides an
// authenticated subject but omits the user service during step-up enablement.
// It checks InternalServerError and retains a value other than true in storage.
func TestUpdateSettingsEnableStepUpFailsClosedWithoutUserService(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": true}, func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1})
	})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotEqual(t, "true", repo.values[identity.SettingKeyStepUpEnabled])
}

// TestUpdateSettingsDisableStepUpRequiresStepUp requests true-to-false step-up
// transition without authentication. It checks Unauthorized and retains the
// stored true value after the privileged transition is rejected.
func TestUpdateSettingsDisableStepUpRequiresStepUp(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyStepUpEnabled: "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": false}, nil)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "true", repo.values[identity.SettingKeyStepUpEnabled])
}

// TestUpdateSettingsDisableStepUpRejectsAdminAPIKey requests step-up
// disablement using an administrator API-Key marker. It checks Forbidden, the
// dedicated rejection reason, and retained enabled state in storage.
func TestUpdateSettingsDisableStepUpRejectsAdminAPIKey(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyStepUpEnabled: "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": false}, func(c *gin.Context) {
		c.Set("auth_method", audit.AuditAuthMethodAdminAPIKey)
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.Equal(t, "true", repo.values[identity.SettingKeyStepUpEnabled])
}

// TestUpdateSettingsStepUpNoTransitionSkipsGate sends false while step-up is
// already absent/disabled. It checks successful saving of false and the
// persisted disabled session-binding default without triggering an enablement
// transition.
func TestUpdateSettingsStepUpNoTransitionSkipsGate(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": false}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyStepUpEnabled])
	// Session IP/User-Agent binding defaults to false and is persisted when omitted.
	require.Equal(t, "false", repo.values[identity.SettingKeySessionBindingEnabled])
}

// TestUpdateSettingsStepUpKeepEnabledSkipsGate sends true when step-up is
// already stored true. It checks successful HTTP retains enabled state,
// exercising a save without a change to that security switch.
func TestUpdateSettingsStepUpKeepEnabledSkipsGate(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyStepUpEnabled: "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[identity.SettingKeyStepUpEnabled])
}

// TestUpdateSettingsOmittedSecuritySwitchesKeepStoredValues updates
// registration while four security switches are stored enabled. It checks
// successful HTTP retains each omitted enabled value rather than changing them
// during an unrelated partial update.
func TestUpdateSettingsOmittedSecuritySwitchesKeepStoredValues(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyStepUpEnabled:                       "true",
		identity.SettingKeySessionBindingEnabled:               "true",
		identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
		identity.SettingKeyUserEmailChangeEnabled:              "true",
	})

	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[identity.SettingKeyStepUpEnabled])
	require.Equal(t, "true", repo.values[identity.SettingKeySessionBindingEnabled])
	require.Equal(t, "true", repo.values[identity.SettingKeyRegistrationEmailDomainQuotaEnabled])
	require.Equal(t, "true", repo.values[identity.SettingKeyUserEmailChangeEnabled])
}

// TestUpdateSettingsOmittedSecuritySwitchesKeepDisabled updates registration
// with four security switches absent. It checks successful HTTP saves their
// disabled defaults, distinguishing the missing-state path from retained
// enabled settings.
func TestUpdateSettingsOmittedSecuritySwitchesKeepDisabled(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyStepUpEnabled])
	require.Equal(t, "false", repo.values[identity.SettingKeySessionBindingEnabled])
	require.Equal(t, "false", repo.values[identity.SettingKeyRegistrationEmailDomainQuotaEnabled])
	require.Equal(t, "false", repo.values[identity.SettingKeyUserEmailChangeEnabled])
}

// TestUpdateSettingsEnablesUserEmailChange sends user_email_change_enabled true
// into an empty settings fixture. It checks successful HTTP and the canonical
// stored true value for that explicit partial update.
func TestUpdateSettingsEnablesUserEmailChange(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	rec := doUpdateSettings(t, h, map[string]any{"user_email_change_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[identity.SettingKeyUserEmailChangeEnabled])
}

// TestUpdateSettingsOmittedDefaultUserAPIKeyLimitKeepsStoredValue updates
// registration while the default Key count is stored. It checks successful HTTP
// retains the omitted limit string for subsequent user creation policy reads.
func TestUpdateSettingsOmittedDefaultUserAPIKeyLimitKeepsStoredValue(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyDefaultUserAPIKeyLimit: "33",
	})

	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "33", repo.values[identity.SettingKeyDefaultUserAPIKeyLimit])
}

// TestUpdateSettingsExplicitZeroDefaultUserAPIKeyLimit sends explicit zero over
// a nonzero default Key limit. It checks successful HTTP stores zero,
// preserving the configured unlimited-count representation for later policy
// interpretation.
func TestUpdateSettingsExplicitZeroDefaultUserAPIKeyLimit(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyDefaultUserAPIKeyLimit: "33",
	})

	rec := doUpdateSettings(t, h, map[string]any{"default_user_api_key_limit": 0}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "0", repo.values[identity.SettingKeyDefaultUserAPIKeyLimit])
}

// TestUpdateSettingsRejectsNegativeDefaultUserAPIKeyLimit sends a negative
// default Key count over a stored limit. It checks BadRequest,
// INVALID_API_KEY_LIMIT, and unchanged stored value after the invalid update.
func TestUpdateSettingsRejectsNegativeDefaultUserAPIKeyLimit(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyDefaultUserAPIKeyLimit: "33",
	})

	rec := doUpdateSettings(t, h, map[string]any{"default_user_api_key_limit": -1}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "INVALID_API_KEY_LIMIT")
	require.Equal(t, "33", repo.values[identity.SettingKeyDefaultUserAPIKeyLimit])
}

// TestUpdateSettingsRejectsDefaultUserAPIKeyLimitAboveDatabaseRange submits one
// beyond the identity maximum default Key limit. It checks BadRequest, the
// dedicated limit reason, and retained prior storage for this range violation.
func TestUpdateSettingsRejectsDefaultUserAPIKeyLimitAboveDatabaseRange(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		identity.SettingKeyDefaultUserAPIKeyLimit: "33",
	})

	rec := doUpdateSettings(t, h, map[string]any{"default_user_api_key_limit": identity.MaxUserAPIKeyLimit + 1}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "INVALID_API_KEY_LIMIT")
	require.Equal(t, "33", repo.values[identity.SettingKeyDefaultUserAPIKeyLimit])
}

// TestUpdateSettingsForwardedClientIPHeadersOmittedPreservesAndEmptyClears
// first omits and then explicitly empties the configured forwarded-header list.
// It checks retained versus cleared JSON in storage and the corresponding
// returned header arrays.
func TestUpdateSettingsForwardedClientIPHeadersOmittedPreservesAndEmptyClears(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		runtimeconfig.SettingKeyForwardedClientIPHeaders: `["X-Cdn-Ip","True-Client-Ip"]`,
	})

	preserved := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)
	require.Equal(t, http.StatusOK, preserved.Code)
	require.JSONEq(t, `["X-Cdn-Ip","True-Client-Ip"]`, repo.values[runtimeconfig.SettingKeyForwardedClientIPHeaders])
	require.Contains(t, preserved.Body.String(), `"forwarded_client_ip_headers":["X-Cdn-Ip","True-Client-Ip"]`)

	cleared := doUpdateSettings(t, h, map[string]any{"forwarded_client_ip_headers": []string{}}, nil)
	require.Equal(t, http.StatusOK, cleared.Code)
	require.JSONEq(t, `[]`, repo.values[runtimeconfig.SettingKeyForwardedClientIPHeaders])
	require.Contains(t, cleared.Body.String(), `"forwarded_client_ip_headers":[]`)
}

// TestUpdateSettingsMalformedForwardedClientIPHeadersRemainFailClosedWhenOmitted
// loads malformed stored headers with forwarded trust requested, then saves an
// unrelated setting. It checks trust stays false and both storage and published
// configuration are normalized to disabled trust with an empty list.
func TestUpdateSettingsMalformedForwardedClientIPHeadersRemainFailClosedWhenOmitted(t *testing.T) {
	cfg := &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}}
	repo := &settingHandlerRepoStub{values: map[string]string{
		runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP: "true",
		runtimeconfig.SettingKeyForwardedClientIPHeaders:  `{"not":"an array"}`,
	}}
	options, store := newCompositeSettingsHTTPFixture(repo, cfg)
	require.ErrorContains(t, provideForwardedSettings(store, cfg).LoadForwardedClientIPSettings(context.Background()), "load forwarded client ip headers")
	require.False(t, cfg.ForwardedClientIPSettings().TrustForwardedIP)
	h := settingshttp.NewHandler(options)

	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP])
	require.JSONEq(t, `[]`, repo.values[runtimeconfig.SettingKeyForwardedClientIPHeaders])
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Empty(t, runtimeSettings.Headers)
	require.Contains(t, rec.Body.String(), `"api_key_acl_trust_forwarded_ip":false`)
	require.Contains(t, rec.Body.String(), `"forwarded_client_ip_headers":[]`)
}

// TestUpdateSettingsRejectsInvalidForwardedClientIPHeader sends a header name
// containing whitespace over a valid stored list. It checks BadRequest and
// unchanged prior JSON, rejecting the invalid name before it can replace trust
// inputs.
func TestUpdateSettingsRejectsInvalidForwardedClientIPHeader(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		runtimeconfig.SettingKeyForwardedClientIPHeaders: `["X-Existing-IP"]`,
	})

	rec := doUpdateSettings(t, h, map[string]any{
		"forwarded_client_ip_headers": []string{"X Invalid"},
	}, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `["X-Existing-IP"]`, repo.values[runtimeconfig.SettingKeyForwardedClientIPHeaders])
}

// TestUpdateSettingsRejectsDeprecatedAdvancedSchedulerFields submits each
// retired experimental scheduler field through combined settings HTTP. It
// checks BadRequest, the dedicated deprecation reason, and that field's name in
// the diagnostic response.
func TestUpdateSettingsRejectsDeprecatedAdvancedSchedulerFields(t *testing.T) {
	for _, field := range []string{
		"advanced_scheduler_enabled",
		"openai_advanced_scheduler_enabled",
		"openai_advanced_scheduler_weight_priority",
	} {
		t.Run(field, func(t *testing.T) {
			h, _ := newStepUpSwitchTestHandler(t, map[string]string{})

			rec := doUpdateSettings(t, h, map[string]any{field: true}, nil)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "DEPRECATED_ADVANCED_SCHEDULER_SETTING")
			require.Contains(t, rec.Body.String(), field)
		})
	}
}

// TestSettingsBStatsDefaultsAndPartialUpdates exercises legacy settings without
// metrics keys and later explicit opt-out changes. Readback preserves omitted
// controls, and attempts to supply an internal UUID cannot overwrite its storage.
func TestSettingsBStatsDefaultsAndPartialUpdates(t *testing.T) {
	id := "fe5233bb-2ed6-4a60-99da-45b42c6c29ec"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{bstats.SettingKeyServerUUID: id})
	rec := doUpdateSettings(t, h, map[string]any{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"bstats_enabled":true`)
	require.Contains(t, rec.Body.String(), `"bstats_log_errors":false`)
	require.Contains(t, rec.Body.String(), `"bstats_log_sent_data":false`)
	require.Contains(t, rec.Body.String(), `"bstats_log_response_status_text":false`)
	require.NotContains(t, rec.Body.String(), id)
	require.NotContains(t, rec.Body.String(), bstats.SettingKeyServerUUID)
	rec = doUpdateSettings(t, h, map[string]any{
		"bstats_enabled":                  false,
		"bstats_log_errors":               true,
		"bstats_log_sent_data":            true,
		"bstats_log_response_status_text": true,
		"bstats_server_uuid":              "forged",
	}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, id, repo.values[bstats.SettingKeyServerUUID])
	rec = doUpdateSettings(t, h, map[string]any{"bstats_log_errors": false}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "false", repo.values[bstats.SettingKeyEnabled])
	require.Equal(t, "false", repo.values[bstats.SettingKeyLogErrors])
	require.Equal(t, "true", repo.values[bstats.SettingKeyLogSentData])
	require.Equal(t, "true", repo.values[bstats.SettingKeyLogResponseStatusText])
}

// TestSettingsCoverAtomicSave publishes an uploaded cover through the real combined handler.
// Failed persistence preserves the previous bytes and descriptor; omission keeps them, while clearing removes both in one committed update.
func TestSettingsCoverAtomicSave(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 3))))
	upload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	repo := &settingAtomicRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{}}}
	options, store := newCompositeSettingsHTTPFixture(repo, &config.Config{UserDefaults: config.UserDefaultsConfig{Concurrency: 5}})
	handler := settingshttp.NewHandler(options)
	response := doUpdateSettings(t, handler, map[string]any{"site_share_image": upload}, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, upload, repo.values[site.SettingKeySiteShareImage])
	require.Equal(t, 2, site.ReadShareImageMetadata(repo.values[site.SettingKeySiteShareImageMeta]).Width)
	metadata := repo.values[site.SettingKeySiteShareImageMeta]
	notified := false
	store.Subscribe(func() { notified = true })
	repo.failWrite = true
	response = doUpdateSettings(t, handler, map[string]any{"site_share_image": ""}, nil)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.False(t, notified)
	require.Equal(t, upload, repo.values[site.SettingKeySiteShareImage])
	require.Equal(t, metadata, repo.values[site.SettingKeySiteShareImageMeta])
	repo.failWrite = false
	response = doUpdateSettings(t, handler, map[string]any{"site_name": "Another name"}, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, upload, repo.values[site.SettingKeySiteShareImage])
	response = doUpdateSettings(t, handler, map[string]any{"site_share_image": ""}, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, repo.values[site.SettingKeySiteShareImage])
	require.Empty(t, site.ReadShareImageMetadata(repo.values[site.SettingKeySiteShareImageMeta]).Hash)
}
