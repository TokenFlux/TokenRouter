package moderation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestIsOpenAICyberWarningText supplies known Cyber warning wording, a Cyber
// link, and ordinary error text through the wrapper. It checks each selected
// recognition result, without querying an external provider policy.
func TestIsOpenAICyberWarningText(t *testing.T) {
	cases := []struct {
		name string // selected classifier scenario label
		text string // supplied warning classifier text
		want bool   // expected returned recognition result
	}{
		{name: "cybersecurity risk", text: "This request may pose a cybersecurity risk", want: true},
		{name: "full trusted access warning", text: "This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request. To get authorized for security work, join the Trusted Access for Cyber program:", want: true},
		{name: "cyber and risk separated", text: "Cyber content was rejected because of risk controls", want: true},
		{name: "cyber link", text: "See https://chatgpt.com/cyber for details", want: true},
		{name: "cyber abuse", text: "Request blocked by cyber abuse policy", want: true},
		{name: "usage policy only", text: "Your request violates our usage policy", want: false},
		{name: "flagged only", text: "The request was flagged by safety systems", want: false},
		{name: "normal error", text: "upstream service unavailable", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, IsOpenAICyberWarningText(tc.text))
		})
	}
}

// TestContentModerationRecordCyberWarning_ExtractsResponsesFailedError supplies
// nested response.failed message evidence under enabled risk control. It checks
// warning creation and the extracted Cyber phrase retained in the fixture
// observation.
func TestContentModerationRecordCyberWarning_ExtractsResponsesFailedError(t *testing.T) {
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(defaultContentModerationConfig())
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(settingRepo, repo, nil, nil, nil, nil, nil)
	svc.Start()

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID: "req_failed",
		UserID:    1001,
		ResponseBody: []byte(`{
			"type":"response.failed",
			"response":{
				"error":{"message":"This request may pose a cybersecurity risk."}
			}
		}`),
	})

	require.NoError(t, err)
	require.NotNil(t, warning)
	require.Len(t, repo.cyberWarnings, 1)
	require.Contains(t, repo.cyberWarnings[0].WarningText, "cybersecurity risk")
}

// TestContentModerationRecordCyberWarning_RecordsCyberPolicyCode supplies a
// nested cyber_policy code and message to warning recording. It checks one
// fixture warning and exact selected text returned from that coded evidence.
func TestContentModerationRecordCyberWarning_RecordsCyberPolicyCode(t *testing.T) {
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(defaultContentModerationConfig())
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(settingRepo, repo, nil, nil, nil, nil, nil)
	svc.Start()

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID: "req_cyber_policy",
		UserID:    1001,
		ResponseBody: []byte(`{
			"response":{
				"error":{
					"code":"cyber_policy",
					"message":"Request blocked by upstream cyber policy"
				}
			}
		}`),
	})

	require.NoError(t, err)
	require.NotNil(t, warning)
	require.Len(t, repo.cyberWarnings, 1)
	require.Equal(t, "Request blocked by upstream cyber policy", repo.cyberWarnings[0].WarningText)
}

// TestContentModerationCyberSessionBlockGroupInScope_RespectsRiskControlGroups
// checks selected, unselected, all-group, and master-disabled combinations in
// fixture settings. It protects the returned session-scope boolean for each
// group policy case.
func TestContentModerationCyberSessionBlockGroupInScope_RespectsRiskControlGroups(t *testing.T) {
	selectedGroupID := int64(101)
	otherGroupID := int64(202)
	tests := []struct {
		name string // selected session-scope scenario label
		// riskControlEnabled, allGroups, groupIDs, and groupID select the fixture
		// master gate and group membership, with want recording their combined
		// expected result. Ordinary audit Enabled remains its default in these
		// Cyber-scope cases.
		riskControlEnabled bool    // fixture risk master switch
		allGroups          bool    // fixture all-group admission switch
		groupIDs           []int64 // fixture selected group identifiers
		groupID            *int64  // queried optional group identifier
		want               bool    // expected session-scope admission result
	}{
		{
			name:               "已选分组启用会话屏蔽",
			riskControlEnabled: true,
			groupIDs:           []int64{selectedGroupID},
			groupID:            &selectedGroupID,
			want:               true,
		},
		{
			name:               "未选分组跳过会话屏蔽",
			riskControlEnabled: true,
			groupIDs:           []int64{selectedGroupID},
			groupID:            &otherGroupID,
			want:               false,
		},
		{
			name:               "全部分组启用会话屏蔽",
			riskControlEnabled: true,
			allGroups:          true,
			groupID:            &otherGroupID,
			want:               true,
		},
		{
			name:               "风控中心关闭时跳过会话屏蔽",
			riskControlEnabled: false,
			allGroups:          true,
			groupID:            &selectedGroupID,
			want:               false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultContentModerationConfig()
			cfg.AllGroups = tc.allGroups
			cfg.GroupIDs = tc.groupIDs
			rawCfg, err := json.Marshal(cfg)
			require.NoError(t, err)
			settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
				SettingKeyRiskControlEnabled:      fmt.Sprintf("%t", tc.riskControlEnabled),
				SettingKeyContentModerationConfig: string(rawCfg),
			}}
			svc := newTestModeration(settingRepo, nil, nil, nil, nil, nil, nil)
			svc.Start()

			inScope, err := svc.CyberSessionBlockGroupInScope(context.Background(), tc.groupID)

			require.NoError(t, err)
			require.Equal(t, tc.want, inScope)
		})
	}
}

// TestContentModerationRecordCyberWarning_SkipsGroupOutOfScope supplies
// recognized Cyber text from a group outside the selected list. It checks a nil
// returned warning and no fixture Cyber insertion.
func TestContentModerationRecordCyberWarning_SkipsGroupOutOfScope(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.AllGroups = false
	cfg.GroupIDs = []int64{101}
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(cfg)
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(settingRepo, repo, nil, nil, nil, nil, nil)
	svc.Start()
	outOfScopeGroupID := int64(202)

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID:      "req_cyber_out_of_group_scope",
		UserID:         1001,
		GroupID:        &outOfScopeGroupID,
		GroupName:      "out-of-scope",
		WarningText:    "This request may pose a cybersecurity risk.",
		UpstreamStatus: 400,
	})

	require.NoError(t, err)
	require.Nil(t, warning)
	require.Empty(t, repo.cyberWarnings)
}

// TestContentModerationRecordCyberWarning_SkipsModelOutOfScope supplies
// recognized Cyber text for a requested model outside an include list. It
// checks a nil returned warning and no fixture Cyber insertion.
func TestContentModerationRecordCyberWarning_SkipsModelOutOfScope(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.ModelFilter = ContentModerationModelFilter{
		Type:   ContentModerationModelFilterInclude,
		Models: []string{"gpt-5"},
	}
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(cfg)
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	repo := &contentModerationTestRepo{}
	svc := newTestModeration(settingRepo, repo, nil, nil, nil, nil, nil)
	svc.Start()

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID:      "req_cyber_out_of_model_scope",
		UserID:         1001,
		Model:          "gpt-4.1",
		WarningText:    "This request may pose a cybersecurity risk.",
		UpstreamStatus: 400,
	})

	require.NoError(t, err)
	require.Nil(t, warning)
	require.Empty(t, repo.cyberWarnings)
}

// TestContentModerationRecordCyberWarning_DefaultRecordsWithoutBan supplies
// warning and prompt text containing recognizable credentials under default
// Cyber policy. It checks raw retained review text, count one, false ban state,
// and no ordinary user updates or invalidation.
func TestContentModerationRecordCyberWarning_DefaultRecordsWithoutBan(t *testing.T) {
	userID := int64(1001)
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(defaultContentModerationConfig())
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	repo := &contentModerationTestRepo{}
	userRepo := &contentModerationTestUserRepo{user: &User{ID: userID, Email: "user@example.com", Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(settingRepo, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID:      "req_1",
		UserID:         userID,
		UserEmail:      "user@example.com",
		ProviderID:     2001,
		ProviderName:   "openai-1",
		UpstreamStatus: 400,
		ResponseBody:   []byte(`{"error":{"message":"This request may pose a cybersecurity risk. token=abc123456789xyz"}}`),
		PromptExcerpt:  "inspect target sk-proj-1234567890abcdef",
	})

	require.NoError(t, err)
	require.NotNil(t, warning)
	require.Len(t, repo.cyberWarnings, 1)
	require.Contains(t, repo.cyberWarnings[0].WarningText, "token=abc123456789xyz")
	require.Contains(t, repo.cyberWarnings[0].PromptExcerpt, "inspect target")
	require.Contains(t, repo.cyberWarnings[0].PromptExcerpt, "sk-proj-1234567890abcdef")
	require.NotContains(t, repo.cyberWarnings[0].PromptExcerpt, "[REDACTED]")
	require.Equal(t, 1, repo.cyberWarnings[0].ViolationCount)
	require.False(t, repo.cyberWarnings[0].AutoBanned)
	require.Empty(t, userRepo.updated)
	require.Empty(t, invalidator.userIDs)
}

// TestContentModerationRecordCyberWarning_BansWhenCyberThresholdReached
// configures a first-warning Cyber threshold in the repository fixture. It
// checks reported AutoBanned and authentication invalidation, while the fixture
// reports policy outcome without writing the separate user-command fixture.
func TestContentModerationRecordCyberWarning_BansWhenCyberThresholdReached(t *testing.T) {
	userID := int64(1001)
	cfg := defaultContentModerationConfig()
	cfg.CyberAutoBanEnabled = true
	cfg.CyberBanThreshold = 1
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(cfg)
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	repo := &contentModerationTestRepo{}
	userRepo := &contentModerationTestUserRepo{user: &User{ID: userID, Email: "user@example.com", Status: StatusActive}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(settingRepo, repo, nil, nil, userRepo, invalidator, nil)
	svc.Start()

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID:      "req_1",
		UserID:         userID,
		UserEmail:      "user@example.com",
		ProviderID:     2001,
		ProviderName:   "openai-1",
		UpstreamStatus: 400,
		WarningText:    "This content was flagged for possible cybersecurity risk.",
	})

	require.NoError(t, err)
	require.NotNil(t, warning)
	require.True(t, warning.AutoBanned)
	require.Empty(t, userRepo.updated)
	require.Equal(t, []int64{userID}, invalidator.userIDs)
}

// TestContentModerationRecordCyberWarning_CountsExistingWindowWarnings preloads
// one recent Cyber fixture observation before submitting another under
// threshold two. It checks count two, a reported ban, and authentication
// invalidation for the actor.
func TestContentModerationRecordCyberWarning_CountsExistingWindowWarnings(t *testing.T) {
	userID := int64(1001)
	cfg := defaultContentModerationConfig()
	cfg.CyberAutoBanEnabled = true
	cfg.CyberBanThreshold = 2
	// The fixture policy uses controlled finite thresholds and JSON-supported
	// values. Ignore encoding error here while this case exercises Cyber
	// recognition and policy outcomes from those supplied settings.
	rawCfg, _ := json.Marshal(cfg)
	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyRiskControlEnabled:      "true",
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	now := time.Now()
	repo := &contentModerationTestRepo{cyberWarnings: []ContentModerationCyberWarning{{
		UserID:         &userID,
		ViolationCount: 1,
		CreatedAt:      now.Add(-time.Hour),
	}}}
	invalidator := &contentModerationTestAuthCacheInvalidator{}
	svc := newTestModeration(settingRepo, repo, nil, nil, nil, invalidator, nil)
	svc.Start()

	warning, err := svc.RecordCyberWarning(context.Background(), ContentModerationCyberWarningInput{
		RequestID:      "req_2",
		UserID:         userID,
		UserEmail:      "user@example.com",
		ProviderID:     2001,
		ProviderName:   "openai-1",
		UpstreamStatus: 400,
		WarningText:    "This request may pose a cybersecurity risk.",
	})

	require.NoError(t, err)
	require.NotNil(t, warning)
	require.Equal(t, 2, warning.ViolationCount)
	require.True(t, warning.AutoBanned)
	require.Equal(t, []int64{userID}, invalidator.userIDs)
}
