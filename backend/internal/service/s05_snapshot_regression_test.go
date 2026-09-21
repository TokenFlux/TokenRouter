package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

// s05IsolationKey 覆盖认证快照的可变引用，凭据与地址均为本地测试数据。
func s05IsolationKey() *apikey.APIKey {
	groupID, teamID, subscriptionID := int64(3), int64(4), int64(5)
	threshold, price := 3.0, 0.2
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return &apikey.APIKey{ID: 1, UserID: 2, Key: "s05-test-only", GroupID: &groupID, TeamID: &teamID, PreferredSubscriptionID: &subscriptionID, ExpiresAt: &now,
		IPWhitelist: []string{"127.0.0.1"}, IPBlacklist: []string{"192.0.2.1"}, ModelMapping: map[string]string{"alias": "original"},
		User: &identity.User{ID: 2, AllowedGroups: []int64{3}, BalanceNotifyThreshold: &threshold, BalanceNotifyExtraEmails: []billing.NotifyEmailSummary{{Email: "test@example.invalid"}}},
		Team: &team.Team{ID: 4}, TeamMembership: &team.TeamMembership{ID: 6, DailyWindowStart: &now},
		Group: &routing.Group{ID: 3, ModelRouting: map[string][]int64{"model": {7, 8}}, SupportedModelScopes: []string{"claude"},
			MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{ExactModelMappings: map[string]string{"alias": "original"}},
			ModelsListConfig:            routing.GroupModelsListConfig{Enabled: true, Models: []string{"model"}}, WebSearchPricePerCall: &price},
	}
}

// TestS05AuthSnapshotIsolation 同一缓存快照的来源和每次物化都不能共享可变状态。
func TestS05AuthSnapshotIsolation(t *testing.T) {
	changes := map[string]func(*apikey.APIKey){
		"group_id":         func(k *apikey.APIKey) { *k.GroupID = 99 },
		"team_id":          func(k *apikey.APIKey) { *k.TeamID = 99 },
		"subscription_id":  func(k *apikey.APIKey) { *k.PreferredSubscriptionID = 99 },
		"expiry":           func(k *apikey.APIKey) { *k.ExpiresAt = k.ExpiresAt.Add(time.Hour) },
		"whitelist":        func(k *apikey.APIKey) { k.IPWhitelist[0] = "changed" },
		"blacklist":        func(k *apikey.APIKey) { k.IPBlacklist[0] = "changed" },
		"model_mapping":    func(k *apikey.APIKey) { k.ModelMapping["alias"] = "changed" },
		"allowed_groups":   func(k *apikey.APIKey) { k.User.AllowedGroups[0] = 99 },
		"notify_threshold": func(k *apikey.APIKey) { *k.User.BalanceNotifyThreshold = 99 },
		"notify_emails":    func(k *apikey.APIKey) { k.User.BalanceNotifyExtraEmails[0].Email = "changed" },
		"member":           func(k *apikey.APIKey) { k.TeamMembership.ID = 99 },
		"member_window": func(k *apikey.APIKey) {
			*k.TeamMembership.DailyWindowStart = k.TeamMembership.DailyWindowStart.Add(time.Hour)
		},
		"routing_map":   func(k *apikey.APIKey) { k.Group.ModelRouting["extra"] = []int64{99} },
		"routing_slice": func(k *apikey.APIKey) { k.Group.ModelRouting["model"][0] = 99 },
		"scopes":        func(k *apikey.APIKey) { k.Group.SupportedModelScopes[0] = "changed" },
		"dispatch":      func(k *apikey.APIKey) { k.Group.MessagesDispatchModelConfig.ExactModelMappings["alias"] = "changed" },
		"models":        func(k *apikey.APIKey) { k.Group.ModelsListConfig.Models[0] = "changed" },
		"price":         func(k *apikey.APIKey) { *k.Group.WebSearchPricePerCall = 99 },
	}
	for name, change := range changes {
		for _, direction := range []string{"source", "request"} {
			t.Run(name+"/"+direction, func(t *testing.T) {
				s := testkit.NewService(nil, nil, nil, nil, nil, nil, nil)
				key := s05IsolationKey()
				snapshot := s.KeySnapshotFromAPIKey(context.Background(), key)
				before, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if direction == "source" {
					change(key)
				} else {
					change(s.KeySnapshotToAPIKey(key.Key, snapshot))
				}
				after, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("请求或来源修改污染了认证缓存快照")
				}
			})
		}
	}
}

// TestS05CompositeSnapshotFastPolicy 复合选组必须保留显式 Fast 策略。
func TestS05CompositeSnapshotFastPolicy(t *testing.T) {
	for _, policy := range []string{"force_off", "force_ultrafast"} {
		t.Run(policy, func(t *testing.T) {
			s := testkit.NewService(nil, nil, nil, nil, nil, nil, nil)
			key := &apikey.APIKey{ID: 1, UserID: 2, User: &identity.User{ID: 2}, IsComposite: true, CompositeGroups: []apikey.APIKeyCompositeGroup{{GroupID: 3, Group: &routing.Group{ID: 3, OpenAIFastPolicy: policy}}}}
			snapshot := s.KeySnapshotFromAPIKey(context.Background(), key)
			payload, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var decoded apikey.APIKeyAuthSnapshot
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			restored := s.KeySnapshotToAPIKey("s05-test-only", &decoded)
			if got := restored.CompositeGroups[0].Group.OpenAIFastPolicy; got != policy {
				t.Fatalf("Fast 策略丢失：got %q want %q", got, policy)
			}
		})
	}
}
