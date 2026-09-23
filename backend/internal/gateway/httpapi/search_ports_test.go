package httpapi

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 替身只代替资金预检与上游执行；HTTP、报文、完成捕获和计费均使用生产实现。
type searchFundingProbe struct{ calls int }

func (p *searchFundingProbe) Check(context.Context, billing.CheckInput) error { p.calls++; return nil }

type searchTargetProbe struct {
	calls     int
	releases  int
	snapshots int
	body      []byte
}

func (p *searchTargetProbe) Select(_ context.Context, group int64, _ string, _ map[int64]struct{}) (StandaloneSearchTarget, searchtools.Selection, bool, error) {
	return p, searchtools.Selection{AccountID: 7, Acquired: true, Release: func() { p.releases++ }}, true, nil
}
func (p *searchTargetProbe) Execute(_ context.Context, body []byte) ([]byte, error) {
	p.calls++
	p.body = append([]byte(nil), body...)
	return []byte(`{"output":[{"type":"web_search_call","action":{"sources":[{"url":"https://source.test","title":"source","snippet":"result"}]}}]}`), nil
}
func (p *searchTargetProbe) CompletionRecord() *account.Record {
	p.snapshots++
	return &account.Record{ID: 7, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}
}

func TestSearchNativePortsCompleteEachRequestOnce(t *testing.T) {
	for _, isX := range []bool{false, true} {
		name := "web"
		if isX {
			name = "x"
		}
		t.Run(name, func(t *testing.T) {
			target := &searchTargetProbe{}
			checks := &searchFundingProbe{}
			logs := &testkit.UsageLogStore{Inserted: true}
			funds := &testkit.SettlementStore{}
			fixture := testkit.NewRecording(logs, funds, nil, false)
			fixture.Dependencies.Calculator = billingtestkit.Calculator(1.1, nil, map[string]*pricing.ModelPricing{"grok-web-search": {}, "grok-x-search": {}})
			recorder := fixture.Core(nil, false)
			ports := SearchPorts{Selector: target, Funding: admission.NewFundingAdmission(checks, nil, nil), Recorder: recorder}
			handler := NewSearchHandler(ports)
			var priorID string
			for i := 1; i <= 2; i++ {
				c, response := searchContext(`{"query":"same query","max_results":3}`)
				groupID := int64(2)
				key := &apikey.APIKey{ID: 3, UserID: 1, User: &identity.User{ID: 1}, GroupID: &groupID, Group: &routing.Group{ID: groupID, Platform: capability.PlatformGrok, RateMultiplier: 1}}
				c.Set(string(keyhttp.ContextKeyAPIKey), key)
				if isX {
					handler.XSearch(c)
				} else {
					handler.WebSearch(c)
				}
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, "https://source.test", gjson.Get(response.Body.String(), "results.0.url").String())
				require.Equal(t, i, target.calls)
				require.Equal(t, i, target.snapshots)
				require.Equal(t, i, target.releases)
				require.Equal(t, i, checks.calls)
				require.Equal(t, i, funds.Calls)
				require.Positive(t, funds.LastCmd.BillableAmountUSD, "搜索按次费用通过实际完成计算器进入唯一资金操作")
				require.Equal(t, i, logs.Calls)
				require.NotNil(t, logs.LastLog)
				require.NotEqual(t, priorID, logs.LastLog.RequestID, "同查询仍按每次请求生成结算标识")
				priorID = logs.LastLog.RequestID
				tool := "web_search"
				if isX {
					tool = "x_search"
				}
				require.Equal(t, tool, gjson.GetBytes(target.body, "tools.0.type").String())
			}
		})
	}
}
