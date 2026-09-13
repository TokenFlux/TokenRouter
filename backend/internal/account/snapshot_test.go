package account

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 候选快照只输出声明字段，修改快照不会反向污染持久配置或关联指针。
func TestAccountRoutingSnapshotExcludesCredentialsAndCopies(t *testing.T) {
	parent := int64(2)
	expires := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	r := &Record{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ParentAccountID: &parent, ExpiresAt: &expires,
		Credentials: map[string]any{"api_key": "private-token-marker", UpstreamProtocolsKey: []string{"openai_responses"}},
		Extra:       map[string]any{"unknown-secret": "private-extra-marker"}}
	snapshot := r.RoutingSnapshot()
	require.Equal(t, []capability.ProtocolID{capability.ProtocolOpenAIResponses}, snapshot.EnabledProtocols)
	payload, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-")
	require.NotContains(t, string(payload), "Credentials")
	*snapshot.ParentAccountID = 3
	*snapshot.ExpiresAt = expires.Add(time.Hour)
	snapshot.EnabledProtocols[0] = capability.ProtocolOpenAIChatCompletions
	require.Equal(t, int64(2), parent)
	require.Equal(t, 0, expires.Hour())
	require.Equal(t, []capability.ProtocolID{capability.ProtocolOpenAIResponses}, r.UpstreamProtocols())
}
