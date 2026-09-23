package postgres

import "testing"

func TestShouldEnqueueSchedulerOutboxForExtraUpdates_CompactConfigurationKeysAreRelevant(t *testing.T) {
	updates := map[string]any{
		"openai_compact_mode":              "force_off",
		"openai_native_compaction_v2_mode": "force_on",
	}

	if !ShouldEnqueueSchedulerOutboxForExtraUpdates(updates) {
		t.Fatalf("expected compact capability updates to enqueue scheduler outbox")
	}
}

func TestShouldEnqueueSchedulerOutboxForExtraUpdates_OpenAITextRouteIsRelevant(t *testing.T) {
	updates := map[string]any{
		"openai_text_route_mode": "force_chat_completions",
	}

	if !ShouldEnqueueSchedulerOutboxForExtraUpdates(updates) {
		t.Fatalf("expected responses capability updates to enqueue scheduler outbox")
	}
}

func TestShouldEnqueueSchedulerOutboxForExtraUpdates_QoderQuotaSnapshotIsNeutral(t *testing.T) {
	updates := map[string]any{
		"qoder_quota_snapshot": map[string]any{
			"user_type": "teams",
			"user_quota": map[string]any{
				"total":     2940,
				"used":      2,
				"remaining": 2938,
			},
		},
		"qoder_quota_updated_at": "2026-07-05T10:00:00Z",
	}

	if ShouldEnqueueSchedulerOutboxForExtraUpdates(updates) {
		t.Fatalf("expected qoder quota snapshot updates to skip scheduler outbox")
	}
}
