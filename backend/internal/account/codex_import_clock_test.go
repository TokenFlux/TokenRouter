package account

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 精确保持两个原有效期边界：JWT exp 的严格大于比较与显式到期值的小于等于比较。
func TestCodexImportInjectedClockAndExpiryBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	options := CodexImportOptions{Now: func() time.Time { return now }, OAuthClientID: "fixture-client"}
	for _, delta := range []int64{-121, -120, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"exp": now.Unix() + delta})
			require.NoError(t, err)
			token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + "."
			item, err := NormalizeCodexImportEntry(CodexImportEntry{Index: 1, Value: map[string]any{"access_token": token, "refresh_token": "fixture-refresh"}}, options)
			if delta < -120 {
				require.ErrorContains(t, err, "access_token 已过期")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "fixture-client", item.Credentials["client_id"])
			item.RefreshToken = ""
			_, _, _, _, err = ResolveCodexImportExpiry(CodexSessionImportRequest{}, item, options.Now)
			if delta <= -120 {
				require.ErrorContains(t, err, "过期时间已过期")
			} else {
				require.NoError(t, err)
			}
		})
	}
	_, _, _, _, err := ResolveCodexImportExpiry(CodexSessionImportRequest{}, &CodexImportAccount{IsAgentIdentity: true}, func() time.Time { t.Fatal("Agent Identity 不应读取 OAuth 有效期时钟"); return now })
	require.NoError(t, err)
}
func TestCodexImportCredentialMergeIndependentNestedValues(t *testing.T) {
	input := map[string]any{"nested": map[string]any{"values": []any{"original"}}, "nil": []any(nil), "empty": []any{}}
	out := MergeCodexImportMap(nil, input)
	nested, ok := out["nested"].(map[string]any)
	require.True(t, ok)
	nested["values"] = []any{"changed"}
	original, ok := input["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"original"}, original["values"])
	require.Equal(t, []any(nil), out["nil"])
	require.Equal(t, []any{}, out["empty"])
}
