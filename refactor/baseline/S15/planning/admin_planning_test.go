package admin
import("testing"; "net/http"; "github.com/TokenFlux/TokenRouter/internal/service"; "github.com/stretchr/testify/require")
// 验证后段校验失败时前段配置是否已经写入。
func TestS15PlanningRejectedFastPolicyHasNoWrites(t *testing.T){
 h,repo:=newStepUpSwitchTestHandler(t,map[string]string{service.SettingKeySiteName:"before"})
 rec:=doUpdateSettings(t,h,map[string]any{"site_name":"after","openai_fast_policy_settings":map[string]any{"rules":[]map[string]any{{"service_tier":"priority","action":"bogus","scope":"all"}}}},nil)
 require.Equal(t,http.StatusBadRequest,rec.Code,rec.Body.String())
 require.Equal(t,"before",repo.values[service.SettingKeySiteName],"后段校验拒绝后不应保存站点名称")
}
