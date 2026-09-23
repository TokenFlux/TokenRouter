package wsentry

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type entryKeyReader struct{ calls int }

func (r *entryKeyReader) GetByKey(context.Context, string) (*apikey.APIKey, error) {
	r.calls++
	return nil, nil
}

// 投影不提前刷新策略；模型和 effort 映射不能因本次连接处理污染原认证快照。
func TestEntryAccessKeepsProjectionIndependentAndLazy(t *testing.T) {
	reader := &entryKeyReader{}
	key := &apikey.APIKey{ID: 9, UserID: 7, Key: "fixture-key", ModelMapping: map[string]string{"alias": "original"}, Group: &routing.Group{ReasoningEffortMappings: []routing.ReasoningEffortMapping{{}}}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	view, ok := (openAIWSHTTPBackend{bindings: Bindings{Keys: reader}}).Access(c)
	require.True(t, ok)
	require.True(t, view.RefreshFastPolicy)
	require.Zero(t, reader.calls)
	view.ModelMapping["alias"] = "changed"
	require.Equal(t, "original", key.ModelMapping["alias"])
	require.NotSame(t, &key.Group.ReasoningEffortMappings[0], &view.Group.ReasoningEffortMappings[0])
}
