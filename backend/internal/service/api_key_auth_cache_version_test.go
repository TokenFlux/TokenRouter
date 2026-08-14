package service

import "testing"

func TestAPIKeyService_RejectsV13AuthSnapshotWithoutSessionIsolationFlag(t *testing.T) {
	groupID := int64(9)
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-models-list", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{
			Version:  13,
			APIKeyID: 1,
			UserID:   2,
			GroupID:  &groupID,
			Status:   StatusActive,
			User: APIKeyAuthUserSnapshot{
				ID:          2,
				Status:      StatusActive,
				Role:        RoleUser,
				Balance:     10,
				Concurrency: 3,
			},
			Group: &APIKeyAuthGroupSnapshot{
				ID:             groupID,
				Name:           "openai",
				Platform:       PlatformOpenAI,
				Status:         StatusActive,
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
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-reasoning-mappings", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 21},
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
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-model-mapping", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 26},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v26 auth snapshot to be rejected after model mapping was added")
	}
}

func TestAPIKeyServiceRejectsV29AuthSnapshotWithoutSchedulerType(t *testing.T) {
	svc := &APIKeyService{}

	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-scheduler-type", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 29},
	})

	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v29 auth snapshot to be rejected after scheduler_type was added")
	}
}

func TestAPIKeyServiceRejectsV30AuthSnapshotWithoutAdvancedSchedulerOverrides(t *testing.T) {
	svc := &APIKeyService{}
	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-advanced-overrides", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 30},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v30 auth snapshot to be rejected after advanced scheduler overrides were added")
	}
}

func TestAPIKeyServiceRejectsV32AuthSnapshotWithoutGroupModelPricing(t *testing.T) {
	svc := &APIKeyService{}
	apiKey, ok, err := svc.applyAuthCacheEntry("k-legacy-group-pricing", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 32},
	})
	if err != nil {
		t.Fatalf("expected stale snapshot to be ignored without error, got %v", err)
	}
	if ok || apiKey != nil {
		t.Fatal("expected v32 auth snapshot to be rejected after group model pricing was added")
	}
}
