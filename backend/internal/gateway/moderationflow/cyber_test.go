package moderationflow

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/stretchr/testify/require"
)

// 捕获队列任务后改写请求数据，证明执行阶段只看到提交时快照。
type queuedCyberTasks struct{ task func() }

func (q *queuedCyberTasks) Go(_ string, fn func()) bool { q.task = fn; return true }

type cyberEffects struct {
	events []string
	entry  *ops.OpsInsertErrorLogInput
	ctx    context.Context
	keys   []string
}

func (e *cyberEffects) MarkCyberSessionBlocked(ctx context.Context, _ string, keys []string) {
	e.events = append(e.events, "block")
	e.ctx = ctx
	e.keys = keys
}
func (e *cyberEffects) Enqueue(in *ops.OpsInsertErrorLogInput) {
	e.events = append(e.events, "ops")
	e.entry = in
}
func TestPolicyDispatchFreezesMetadataAndKeepsIndependentBudget(t *testing.T) {
	q := &queuedCyberTasks{}
	effects := &cyberEffects{}
	runtime := Runtime{Tasks: q, Blocks: effects, Ops: effects}
	group := int64(4)
	trace := modeltrace.NewAPIKeyModelRedirectTrace("original", "original", "mapped")
	request, cancel := context.WithCancel(modeltrace.WithContext(context.Background(), trace))
	in := PolicyCompletion{Meta: OpsMeta{GroupID: &group, Model: "original"}, Mark: Mark{Message: "observed", Body: "body", UpstreamStatus: 403}, BlockKey: "scope"}
	runtime.Dispatch(request, in)
	group = 8
	in.Mark.Message = "changed"
	in.Meta.Model = "changed"
	trace.ClientModel = "changed"
	cancel()
	require.NotNil(t, q.task)
	q.task()
	require.Equal(t, []string{"block", "ops"}, effects.events)
	require.Equal(t, int64(4), *effects.entry.GroupID)
	require.Equal(t, "original", effects.entry.Model)
	require.Equal(t, "cyber_policy: observed", effects.entry.ErrorMessage)
	require.Equal(t, []string{"scope"}, effects.keys)
	saved, ok := modeltrace.FromContext(effects.ctx)
	require.True(t, ok)
	require.Equal(t, "original", saved.ClientModel)
	deadline, ok := effects.ctx.Deadline()
	require.True(t, ok)
	require.InDelta(t, 30, time.Until(deadline).Seconds(), 1)
}
func TestPolicyBlockPlanRetainsOriginalKeyOrdering(t *testing.T) {
	p := BuildBlockPlan("explicit", []string{"explicit", "old", "new"}, "scope")
	require.Equal(t, BlockPlan{ScopeKey: "scope", Keys: []string{"explicit", "old", "new"}}, p)
	require.Equal(t, BlockPlan{Keys: []string{"explicit"}}, BuildBlockPlan("explicit", nil, "unused"))
}
func TestModerationContentSnapshotIsolatesAllCollections(t *testing.T) {
	in := moderation.ContentModerationInput{Images: []string{"image"}, Items: []moderation.ContentModerationInputItem{{Text: "tool"}}, ImageItems: []moderation.ContentModerationImage{{Reference: "ref"}}}
	out := SnapshotContent(in)
	in.Images[0] = "changed"
	in.Items[0].Text = "changed"
	in.ImageItems[0].Reference = "changed"
	require.Equal(t, "image", out.Images[0])
	require.Equal(t, "tool", out.Items[0].Text)
	require.Equal(t, "ref", out.ImageItems[0].Reference)
}
