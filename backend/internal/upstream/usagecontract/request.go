// 通用查询适配器只接收本次技术输入，凭据不能序列化或进入调试输出。
package usagecontract

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

type Request struct {
	BaseURL                                                            string
	APIKey, WalletToken, WalletUserID, ZhipuOrganization, ZhipuProject string                                      `json:"-"`
	Do                                                                 func(*http.Request) (*http.Response, error) `json:"-"`
	Context                                                            func(context.Context) context.Context       `json:"-"`
	ApplyHeaders                                                       func(http.Header)                           `json:"-"`
	Endpoint                                                           func(string, string) string                 `json:"-"`
}

func (r *Request) String() string   { return "upstream usage request" }
func (r *Request) GoString() string { return r.String() }

// Adapter 每次查询创建自己的请求读取器，不拥有账号缓存或健康写入。
type Adapter interface {
	Name() string
	Query(context.Context, *Request) (*usageview.UpstreamUsageInfo, error)
}
