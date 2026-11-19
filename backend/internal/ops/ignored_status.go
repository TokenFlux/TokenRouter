package ops

import (
	"context"
	"encoding/json"
	"sort"
)

// ignored_status.go normalizes client-status exclusions and resolves persisted
// ignored-status lists for SLA queries and policy comparisons. Nil input
// selects the default authentication statuses, 401 and 403, while an allocated
// empty slice disables status exclusions. Nonempty input retains every supplied
// integer, sorted and deduplicated for query construction.
//
// Normalization allocates a result before sorting, so comparisons and SQL
// construction can use a stable ordering without rearranging caller input.
// Repository resolution reads only SettingKeyOpsAdvancedSettings through
// SettingValueReader, falling back to freshly allocated defaults when the
// reader is nil, unavailable, or holds malformed JSON.

// DefaultOpsIgnoredStatusCodes allocates the default client-status exclusions
// for Ops SLA queries.
func DefaultOpsIgnoredStatusCodes() []int {
	return []int{401, 403}
}

// resolveOpsIgnoredStatusCodesFromRepo loads persisted advanced JSON over its
// defaults and returns an independent ignored-status slice. Missing repository,
// any read error, or malformed JSON selects default 401/403; an allocated empty
// decoded policy remains empty after normalization. Nil context selects
// background for the settings read.
//
// This query-policy read is independent of OpsAdvancedSettingsSnapshot, which
// supplies cached capture preferences. A persisted read and a snapshot read
// need not represent the same update, so the result defines classification for
// its caller rather than synchronizing the hot-path snapshot.
func resolveOpsIgnoredStatusCodesFromRepo(ctx context.Context, repo SettingValueReader) []int {
	if repo == nil {
		return DefaultOpsIgnoredStatusCodes()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := repo.GetValue(ctx, SettingKeyOpsAdvancedSettings)
	// Any unavailable persisted policy selects fresh default exclusions for this
	// query. This path deliberately reads storage independently of the service's
	// published advanced snapshot.
	if err != nil {
		return DefaultOpsIgnoredStatusCodes()
	}
	cfg := defaultOpsAdvancedSettings()
	// Decode over defaults so omitted exclusion fields retain their default
	// policy. Malformed JSON discards the decoded attempt and returns
	// independently allocated defaults.
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return DefaultOpsIgnoredStatusCodes()
	}
	normalizeOpsAdvancedSettings(cfg)
	return cloneOpsIgnoredStatusCodes(cfg.IgnoredStatusCodes)
}

// NormalizeOpsIgnoredStatusCodes returns independently allocated, sorted,
// unique exclusion codes. Nil input selects the default 401 and 403 policy; an
// allocated empty slice selects no status exclusions, and integer validity is
// checked by settings updates.
func NormalizeOpsIgnoredStatusCodes(codes []int) []int {
	if codes == nil {
		return DefaultOpsIgnoredStatusCodes()
	}
	if len(codes) == 0 {
		return []int{}
	}
	seen := make(map[int]struct{}, len(codes))
	out := make([]int, 0, len(codes))
	for _, code := range codes {
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	sort.Ints(out)
	return out
}
