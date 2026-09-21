package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// QoderRuntimeOptions 只绑定账号令牌、技术传输及已有平台会话，不持有旧实体或完整配置。
type QoderRuntimeOptions struct {
	Tokens        *accountprovider.QoderTokenProvider
	Client        qoder.StreamClient
	Transport     accountprovider.QoderTransport
	Profiles      *egressprovider.TLSProfiles
	Health        accountprovider.QoderHealthStore
	Conversations *qoder.QoderConversationStore
}

// QoderRuntime 持有唯一平台执行器和会话状态；账号选择、资金与全局重试在调用方。
type QoderRuntime struct {
	options       QoderRuntimeOptions
	mu            sync.Mutex
	conversations *qoder.QoderConversationStore
	executor      *qoder.Executor
	enter         func() (func(), error)
}

// NewQoderRuntime 构造不启动后台任务，保留已有会话存储的作用域。
func NewQoderRuntime(options QoderRuntimeOptions) *QoderRuntime {
	conversations := options.Conversations
	if conversations == nil {
		conversations = qoder.NewQoderConversationStore(qoder.QoderConversationTTL)
	}
	return &QoderRuntime{options: options, conversations: conversations}
}

// BindAttemptActivity 在开放入口前绑定应用拥有者，保持首次执行器创建时固化的回调。
func (r *QoderRuntime) BindAttemptActivity(enter func() (func(), error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enter = enter
}

// Executor 只创建一次平台执行器，不创建第二套账号尝试循环。
func (r *QoderRuntime) Executor() *qoder.Executor {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executor == nil {
		r.executor = qoder.NewExecutor(qoder.ExecuteOptions{Conversations: r.conversations, Enter: r.enter})
	}
	return r.executor
}

// PrepareQoderTarget 将本次账号记录投影为受控目标，凭据不进入公开结果。
func (r *QoderRuntime) PrepareQoderTarget(metadata qoder.RequestMetadata, value *account.Record, body []byte, wire protocol.ProtocolID, responseModel string) (upstream.Executor, upstream.AttemptInput) {
	return r.Executor(), upstream.AttemptInput{Protocol: wire, Body: mapQoderRequestModel(value, body), ResponseModel: responseModel, Stream: qoder.GjsonBool(body, "stream"), Target: r.Target(metadata, value)}
}

// Target 保留会话与客户端的按需取得时点，未在准备阶段发起网络请求。
func (r *QoderRuntime) Target(metadata qoder.RequestMetadata, value *account.Record) *qoder.Target {
	site, err := qoderRuntimeSite(value)
	if err != nil {
		site = qoder.SiteGlobal
	}
	id := int64(0)
	userType := "personal_standard"
	if value != nil {
		id = value.ID
		userType = qoder.FirstNonEmptyQoder(value.GetCredential("user_type"), userType)
	}
	return &qoder.Target{AccountID: id, Site: site, UserType: userType, Metadata: metadata,
		Session: func(ctx context.Context) (*qoder.SessionContext, error) {
			return r.options.Tokens.GetSession(ctx, value)
		},
		Client: func() (qoder.StreamClient, error) {
			if r.options.Client != nil {
				if _, production := r.options.Client.(*qoder.Client); !production {
					return r.options.Client, nil
				}
			}
			resolved, err := qoderRuntimeSite(value)
			if err != nil {
				return nil, err
			}
			profile, err := qoder.ProfileForSite(resolved)
			if err != nil {
				return nil, err
			}
			return qoder.NewClientForProfile(profile), nil
		},
		Doer: accountprovider.QoderRequestDoer(value, r.options.Transport, r.options.Profiles),
	}
}

// ObserveQoderFailure 只转交原账号健康观察，不提交用量或决定重试。
func (r *QoderRuntime) ObserveQoderFailure(ctx context.Context, value *account.Record, err error) {
	if r == nil || value == nil {
		return
	}
	accountprovider.ObserveQoderUpstreamError(ctx, value.ID, r.options.Health, err)
}

func qoderRuntimeSite(value *account.Record) (qoder.Site, error) {
	if value == nil {
		return qoder.SiteGlobal, fmt.Errorf("qoder: account is nil")
	}
	return qoder.ParseSite(value.GetCredential("site"))
}

func mapQoderRequestModel(value *account.Record, body []byte) []byte {
	if value == nil || !value.IsQoder() || len(body) == 0 {
		return body
	}
	model := strings.TrimSpace(qoder.GjsonString(body, "model"))
	if model == "" {
		return body
	}
	mapped, matched := account.ResolveMappedModel(value.Platform, account.ResolveModelMapping(value, accountprovider.ModelDefaults()), model)
	if !matched || mapped == "" || mapped == model {
		return body
	}
	return openaiwire.ReplaceModelInBody(body, mapped)
}
