package compact

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

// 记录各端口调用，确认提取后仍延迟读取全局模型且按原次序释放旧响应。
type recoveryPorts struct {
	calls        []string
	accountModel string
}

func (p *recoveryPorts) AccountModel(model string) (string, bool) {
	p.calls = append(p.calls, "account:"+model)
	return p.accountModel, p.accountModel != ""
}
func (p *recoveryPorts) GlobalModel() string {
	p.calls = append(p.calls, "global")
	return "global-fallback"
}
func (p *recoveryPorts) ResolveGlobalModel(model string) string {
	p.calls = append(p.calls, "resolve:"+model)
	return model
}
func (p *recoveryPorts) ObserveRetry(_ []byte, _ string) { p.calls = append(p.calls, "observe") }
func (p *recoveryPorts) CloseResponse()                  { p.calls = append(p.calls, "close") }
func (p *recoveryPorts) SetModel(model string)           { p.calls = append(p.calls, "model:"+model) }
func (p *recoveryPorts) LogRetry(from, to, code string) {
	p.calls = append(p.calls, "log:"+from+":"+to+":"+code)
}

func TestRecoveryPortOrderAndSingleRetry(t *testing.T) {
	for _, tc := range []struct {
		name, accountModel, fallback string
		reads                        []string
	}{
		{name: "account", accountModel: "account-fallback", fallback: "account-fallback", reads: []string{"account:client"}},
		{name: "global", fallback: "global-fallback", reads: []string{"account:client", "global", "resolve:global-fallback"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recoveryPorts{accountModel: tc.accountModel}
			r := Recovery{
				Models:        p,
				ContextWindow: func(string, []byte) bool { return false },
				RewriteModel: func(body []byte, model string) []byte {
					v, err := sjson.SetBytes(body, "model", model)
					require.NoError(t, err)
					return v
				},
			}
			body := []byte(`{"model":"current","input":[{"type":"compaction_trigger"}],"stream":true}`)
			in := Request{Explicit: true, RequestedModel: "client", Body: body}
			failure := r.NewFailure(true, []byte(`{"error":{"code":"model_not_found"}}`), "model not found")
			result, model, ok := r.ApplySignal(in, failure, p)
			require.True(t, ok)
			require.Equal(t, tc.fallback, model)
			require.JSONEq(t, `{"model":"`+model+`","input":[{"type":"compaction_trigger"}],"stream":true}`, string(result))
			require.Equal(t, append(tc.reads, "observe", "close", "model:"+model, "log:current:"+model+":model_not_found"), p.calls)
			p.calls = nil
			in.Body = result
			in.AlreadyRetried = true
			unchanged, _, retry := r.ApplySignal(in, failure, p)
			require.False(t, retry)
			require.Equal(t, result, unchanged)
			require.Empty(t, p.calls)
		})
	}
}

func TestStreamPayloadInjectsIDOnlyWhenRequired(t *testing.T) {
	calls := 0
	id := func() string { calls++; return "resp_generated" }
	_, ok := StreamPayload([]byte(`{"id":"resp_existing","output":[]}`), id)
	require.True(t, ok)
	require.Zero(t, calls)
	result, ok := StreamPayload([]byte(`{"output":[]}`), id)
	require.True(t, ok)
	require.Equal(t, 1, calls)
	require.Contains(t, string(result), `"id":"resp_generated"`)
}
