package routing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// rejectionRulesProbe 记录规则读取时点，避免提取后提前读取不合资格账号的配置。
type rejectionRulesProbe struct {
	eligible, mixed bool
	configured      []string
	allowed         bool
	calls           *[]string
}

func (r rejectionRulesProbe) IsSchedulable() bool {
	*r.calls = append(*r.calls, "eligible")
	return r.eligible
}
func (r rejectionRulesProbe) IsMixedSchedulingEnabled() bool {
	*r.calls = append(*r.calls, "mixed")
	return r.mixed
}
func (r rejectionRulesProbe) GetConfiguredRequestModels() []string {
	*r.calls = append(*r.calls, "configured")
	return r.configured
}
func (r rejectionRulesProbe) IsModelSupported(string) bool {
	*r.calls = append(*r.calls, "supports")
	return r.allowed
}

func TestModelRejectionPreservesLazyReadAndEmptyShape(t *testing.T) {
	t.Run("不读取不合资格账号", func(t *testing.T) {
		var calls []string
		sources := []ModelRejectionSource{{Platform: "qoder", Rules: rejectionRulesProbe{calls: &calls}, Defaults: func(string) ([]string, error) { t.Fatal("不应读取默认目录"); return nil, nil }}}
		require.Nil(t, AvailableModelsForRejection(sources, "qoder"))
		require.Equal(t, []string{"eligible"}, calls)
	})
	t.Run("显式模型过滤为空仍为非nil", func(t *testing.T) {
		var calls []string
		sources := []ModelRejectionSource{{Platform: "qoder", Rules: rejectionRulesProbe{eligible: true, configured: []string{"blocked"}, calls: &calls}, Defaults: func(string) ([]string, error) { t.Fatal("不应读取默认目录"); return nil, nil }}}
		result := AvailableModelsForRejection(sources, "qoder")
		require.NotNil(t, result)
		require.Empty(t, result)
		require.Equal(t, []string{"eligible", "configured", "supports"}, calls)
	})
	t.Run("空请求不读账号规则", func(t *testing.T) {
		var calls []string
		require.NoError(t, NewGroupModelRejection("qoder", " ", []ModelRejectionSource{{Platform: "qoder", Rules: rejectionRulesProbe{calls: &calls}}}))
		require.Empty(t, calls)
	})
}
