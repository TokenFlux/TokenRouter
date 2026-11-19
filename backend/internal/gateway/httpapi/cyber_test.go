package httpapi

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
)

// Cyber handling separates early session writes, warning scope, and deduplicated
// completion dispatch. The recording fixture exposes callback order and delayed
// Ops input so these cases can observe which effects occur before scope
// rejection and which request values survive later caller mutation.

// newCyberTest binds one recording fixture across cyber policy and runtime operations.
func newCyberTest(p *cyberTestPorts) *CyberHandler {
	return NewCyberHandler(p, p, p, moderationflow.Runtime{Tasks: p, Blocks: p, Ops: p})
}

// TestCyberPolicyScopeDedupAndBackgroundOrder records in-scope evidence twice
// before executing its captured task. Immediate callback order must include
// repeated early block work while warning and dispatch remain deduplicated;
// delayed Ops output must retain the first model and body observations.
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

// TestCyberPolicyOutOfScopeRetainsOnlyEarlySessionWrite supplies absent scope
// or a scope-read error after a block plan. Both cases must retain the
// early block and scope callbacks, leave the handled flag false, and submit no task.
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

// TestCyberSessionBlockPreservesScopeAndDedicatedOps finds a blocked session
// after enablement and group scope. The response must be forbidden with
// the stable block code and dedicated Ops marker; an out-of-scope case
// must stop its trace before lookup or Ops submission.
func TestCyberSessionBlockPreservesScopeAndDedicatedOps(t *testing.T) {
	p := &cyberTestPorts{enabled: true, scope: true, found: "blocked"}
	c, w := prefaceContext("body")
	require.True(t, newCyberTest(p).RejectSession(c, prefaceKey(), nil, "model", CyberBlockChat))
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "session_blocked_by_cyber_policy")
	require.Equal(t, []string{"enabled", "group", "find", "ops"}, p.events)
	require.True(t, IsOpsDedicatedErrorRecorded(c))
	p = &cyberTestPorts{enabled: true, scope: false}
	c, _ = prefaceContext("body")
	require.False(t, newCyberTest(p).RejectSession(c, prefaceKey(), nil, "model", CyberBlockResponses))
	require.Equal(t, []string{"enabled", "group"}, p.events)
}

// TestOrdinaryModerationFailureRemainsFailOpen supplies a moderation Check
// error through the fixture. The wrapper must return no decision and leave
// its callback trace empty for this request, allowing the caller to continue preparation.
func TestOrdinaryModerationFailureRemainsFailOpen(t *testing.T) {
	p := &cyberTestPorts{}
	c, _ := prefaceContext("body")
	require.Nil(t, RunContentModeration(p, c, nil, p, prefaceKey(), authctx.AuthSubject{UserID: 42}, "openai_responses", "m", []byte("body")))
	require.Empty(t, p.events)
}
