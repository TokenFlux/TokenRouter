package apikey_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func TestAPIKeyService_RejectsV13AuthSnapshotWithoutSessionIsolationFlag(t *testing.T) {
	groupID := int64(9)
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-models-list", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{
			Version:  13,
			APIKeyID: 1,
			UserID:   2,
			GroupID:  &groupID,
			Status:   billing.StatusActive,
			User: apikey.APIKeyAuthUserSnapshot{
				ID:          2,
				Status:      billing.StatusActive,
				Role:        identity.RoleUser,
				Balance:     10,
				Concurrency: 3,
			},
			Group: &apikey.APIKeyAuthGroupSnapshot{
				ID:             groupID,
				Name:           "openai",
				Platform:       capability.PlatformOpenAI,
				Status:         billing.StatusActive,
				RateMultiplier: 1,
			},
		},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok {
		t.Fatalf("expected v13 auth snapshot to be rejected after session isolation flag was added")
	}
	if apiKey != nil {
		t.Fatalf("expected no API key from stale snapshot, got %#v", apiKey)
	}
}

func TestAPIKeyService_RejectsV21AuthSnapshotWithoutReasoningEffortPolicy(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-reasoning-mappings", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 21},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok {
		t.Fatal("expected v21 auth snapshot to be rejected after reasoning effort policy was added")
	}
	if apiKey != nil {
		t.Fatalf("expected no API key from stale snapshot, got %#v", apiKey)
	}
}

func TestAPIKeyServiceRejectsV26AuthSnapshotWithoutModelMapping(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-model-mapping", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 26},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v26 auth snapshot to be rejected after model mapping was added")
	}
}

func TestAPIKeyServiceRejectsV29AuthSnapshotWithoutSchedulerType(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})

	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-scheduler-type", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 29},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v29 auth snapshot to be rejected after scheduler_type was added")
	}
}

func TestAPIKeyServiceRejectsV30AuthSnapshotWithoutAdvancedSchedulerOverrides(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})
	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-advanced-overrides", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 30},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v30 auth snapshot to be rejected after advanced scheduler overrides were added")
	}
}

func TestAPIKeyServiceRejectsV32AuthSnapshotWithoutGroupModelPricing(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})
	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-group-pricing", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 32},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v32 auth snapshot to be rejected after group model pricing was added")
	}
}

// TestAPIKeyServiceRejectsV33AuthSnapshotWithoutGroupOpenAIFast ensures old
// snapshots cannot silently omit the group-level Fast policy.
func TestAPIKeyServiceRejectsV33AuthSnapshotWithoutGroupOpenAIFast(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})
	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-group-openai-fast", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 33},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v33 auth snapshot to be rejected after group OpenAI Fast was added")
	}
}

// TestAPIKeyServiceRejectsV34AuthSnapshotWithoutReasoningEffortOverLimit 验证旧快照不会缺少超限动作。
func TestAPIKeyServiceRejectsV34AuthSnapshotWithoutReasoningEffortOverLimit(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})
	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-reasoning-over-limit", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 34},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v34 auth snapshot to be rejected after reasoning effort over-limit action was added")
	}
}

// TestAPIKeyServiceRejectsV35AuthSnapshotWithoutFreeOpenAIFast 验证旧快照不会缺少免费 Fast 策略。
func TestAPIKeyServiceRejectsV35AuthSnapshotWithoutFreeOpenAIFast(t *testing.T) {
	svc := newAPIKeyTestService(apiKeyTestDependencies{})
	apiKey, ok, err := svc.KeyApplyAuthCacheEntry("k-legacy-free-openai-fast", &apikey.APIKeyAuthCacheEntry{
		Snapshot: &apikey.APIKeyAuthSnapshot{Version: 35},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v35 auth snapshot to be rejected after free OpenAI Fast was added")
	}
}
