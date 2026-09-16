package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 单步替身观察调用顺序；未实现端口若误入会立即失败。
type cyberTestPorts struct {
	CyberBackend
	ModerationPort
	events   []string
	scope    bool
	scopeErr error
	mark     moderationflow.Mark
	task     func()
	entry    *ops.OpsInsertErrorLogInput
	enabled  bool
	found    string
}

func (p *cyberTestPorts) Mark(*gin.Context) *moderationflow.Mark       { return &p.mark }
func (p *cyberTestPorts) UpstreamEndpoint(*gin.Context, string) string { return "/v1/responses" }
func (p *cyberTestPorts) Inbound(*gin.Context) string                  { return "/v1/responses" }
func (p *cyberTestPorts) Forced(*gin.Context) (string, bool)           { return "", false }
func (p *cyberTestPorts) CyberWarningInScope(context.Context, moderation.ContentModerationCyberWarningInput) (bool, error) {
	p.events = append(p.events, "scope")
	return p.scope, p.scopeErr
}
func (p *cyberTestPorts) RecordCyberWarning(context.Context, moderation.ContentModerationCyberWarningInput) (*moderation.ContentModerationCyberWarning, error) {
	p.events = append(p.events, "warning")
	return &moderation.ContentModerationCyberWarning{ID: 6}, nil
}
func (p *cyberTestPorts) MarkCyberSessionBlocked(context.Context, string, []string) {
	p.events = append(p.events, "block")
}
func (p *cyberTestPorts) Go(_ string, fn func()) bool {
	p.events = append(p.events, "submit")
	p.task = fn
	return true
}
func (p *cyberTestPorts) Enqueue(e *ops.OpsInsertErrorLogInput) {
	p.events = append(p.events, "ops")
	p.entry = e
}
func (p *cyberTestPorts) Available() bool { return true }
func (p *cyberTestPorts) Enabled(context.Context) bool {
	p.events = append(p.events, "enabled")
	return p.enabled
}
func (p *cyberTestPorts) CyberSessionBlockGroupInScope(context.Context, *int64) (bool, error) {
	p.events = append(p.events, "group")
	return p.scope, p.scopeErr
}
func (p *cyberTestPorts) Find(context.Context, int64, *gin.Context, []byte) string {
	p.events = append(p.events, "find")
	return p.found
}
func (p *cyberTestPorts) StopKeepalive(*gin.Context) bool { return false }
func (p *cyberTestPorts) Check(context.Context, moderation.ContentModerationCheckInput) (*moderation.ContentModerationDecision, error) {
	return nil, errors.New("failed")
}
func newCyberTest(p *cyberTestPorts) *CyberHandler {
	return NewCyberHandler(p, p, p, moderationflow.Runtime{Tasks: p, Blocks: p, Ops: p})
}
func TestCyberPolicyScopeDedupAndBackgroundOrder(t *testing.T) {
	p := &cyberTestPorts{scope: true, mark: moderationflow.Mark{Message: "warning", Body: "original", UpstreamStatus: 403}}
	h := newCyberTest(p)
	c, _ := prefaceContext("body")
	call := CyberPolicyCall{Key: prefaceKey(), Model: "model", Plan: moderationflow.BlockPlan{ScopeKey: "scope", Keys: []string{"key"}}, HasPlan: true}
	require.True(t, h.RecordPolicy(c, call))
	require.Equal(t, []string{"block", "scope", "warning", "submit"}, p.events)
	require.True(t, c.GetBool(CyberPolicyRecordedKey))
	require.True(t, c.GetBool(CyberWarningRecordedKey))
	p.mark.Body = "changed"
	call.Model = "changed"
	require.True(t, h.RecordPolicy(c, call))
	require.Equal(t, []string{"block", "scope", "warning", "submit", "block"}, p.events)
	p.task()
	require.Equal(t, "original", p.entry.ErrorBody)
	require.Equal(t, "model", p.entry.Model)
	require.Equal(t, []string{"block", "ops"}, p.events[len(p.events)-2:])
}
func TestCyberPolicyOutOfScopeRetainsOnlyEarlySessionWrite(t *testing.T) {
	for _, scopeErr := range []error{nil, errors.New("scope unavailable")} {
		p := &cyberTestPorts{scopeErr: scopeErr}
		h := newCyberTest(p)
		c, _ := prefaceContext("body")
		require.False(t, h.RecordPolicy(c, CyberPolicyCall{Key: prefaceKey(), HasPlan: true, Plan: moderationflow.BlockPlan{Keys: []string{"key"}}}))
		require.Equal(t, []string{"block", "scope"}, p.events)
		require.False(t, c.GetBool(CyberPolicyRecordedKey))
		require.Nil(t, p.task)
	}
}
func TestCyberSessionBlockPreservesScopeAndDedicatedOps(t *testing.T) {
	p := &cyberTestPorts{enabled: true, scope: true, found: "blocked"}
	c, w := prefaceContext("body")
	require.True(t, newCyberTest(p).RejectSession(c, prefaceKey(), nil, "model", CyberBlockChat))
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "session_blocked_by_cyber_policy")
	require.Equal(t, []string{"enabled", "group", "find", "ops"}, p.events)
	require.True(t, c.GetBool("ops_dedicated_error_recorded"))
	p = &cyberTestPorts{enabled: true, scope: false}
	c, _ = prefaceContext("body")
	require.False(t, newCyberTest(p).RejectSession(c, prefaceKey(), nil, "model", CyberBlockResponses))
	require.Equal(t, []string{"enabled", "group"}, p.events)
}
func TestOrdinaryModerationFailureRemainsFailOpen(t *testing.T) {
	p := &cyberTestPorts{}
	c, _ := prefaceContext("body")
	require.Nil(t, RunContentModeration(p, c, nil, p, prefaceKey(), authctx.AuthSubject{UserID: 42}, "openai_responses", "m", []byte("body")))
	require.Empty(t, p.events)
}
