package account

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const (
	openAITeamLinkedErrorDedupTTL      = 60 * time.Second
	openAITeamLinkedErrorFanoutTimeout = 30 * time.Second
	OpenAITeamLinkedErrorBlockReason   = "team_linked_error"
)

// HandleWorkspaceDeactivated 在 OpenAI OAuth 账户收到 402 deactivated_workspace
// （ChatGPT Team 工作区被停用）时，把同一 Team（credentials.chatgpt_account_id 相同）
// 的其余 active 账户一并置为 error 并立即熔断。触发账户自身不在 fan-out 范围内，
// 仍由常规 402 处理标记。
func (s *TeamLinkedHealth) HandleWorkspaceDeactivated(ctx context.Context, account *Record, deactivated bool) {
	if s == nil || s.accountRepo == nil || !deactivated || account == nil || !account.IsOpenAIOAuthLike() {
		return
	}
	teamID := strings.TrimSpace(account.GetChatGPTAccountID())
	if teamID == "" {
		return
	}
	if !s.markOpenAITeamLinkedFired(teamID) {
		return
	}
	// 上游报错场景请求 ctx 往往已被取消，落库需要独立生命周期。
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	opCtx, cancel := context.WithTimeout(baseCtx, openAITeamLinkedErrorFanoutTimeout)
	defer cancel()

	accounts, err := s.accountRepo.ListByPlatform(opCtx, capability.PlatformOpenAI)
	if err != nil {
		s.options.Warn("openai_team_linked_error_list_failed", "trigger_account_id", account.ID, "error", err)
		return
	}
	var targets []*Record
	for i := range accounts {
		acc := &accounts[i]
		if acc.ID == account.ID || acc.IsShadow() || strings.TrimSpace(acc.GetChatGPTAccountID()) != teamID {
			continue
		}
		targets = append(targets, acc)
	}
	if len(targets) == 0 {
		return
	}
	// 先全部进程内熔断（微秒级生效），再逐个落库，避免后面的账户等待前面的 DB 写入。
	for _, acc := range targets {
		s.options.Block(acc, time.Time{}, OpenAITeamLinkedErrorBlockReason)
	}
	errorMsg := fmt.Sprintf("Workspace deactivated (402): team-linked error triggered by account #%d", account.ID)
	marked := 0
	for _, acc := range targets {
		// 单账户写入失败不中断其余账户；进程内熔断已先行，且该账户仍为 active，
		// 下一个 402 在去重 TTL 过期后会重新触发 fan-out。
		if err := s.accountRepo.SetError(opCtx, acc.ID, errorMsg); err != nil {
			s.options.Warn("openai_team_linked_error_set_error_failed", "account_id", acc.ID, "error", err)
			continue
		}
		marked++
	}
	s.options.Warn("openai_team_linked_error_fanout",
		"trigger_account_id", account.ID,
		"chatgpt_account_id", teamID,
		"affected", marked,
		"targets", len(targets),
	)
}

// markOpenAITeamLinkedFired 以 teamID 为键做进程内去重：TTL 内同一 Team 只允许一次 fan-out。
func (s *TeamLinkedHealth) markOpenAITeamLinkedFired(teamID string) bool {
	now := s.options.Now()
	s.openaiTeamLinkedMu.Lock()
	defer s.openaiTeamLinkedMu.Unlock()
	if expiry, ok := s.openaiTeamLinkedRecent[teamID]; ok && expiry.After(now) {
		return false
	}
	if s.openaiTeamLinkedRecent == nil {
		s.openaiTeamLinkedRecent = make(map[string]time.Time)
	}
	for k, v := range s.openaiTeamLinkedRecent {
		if !v.After(now) {
			delete(s.openaiTeamLinkedRecent, k)
		}
	}
	s.openaiTeamLinkedRecent[teamID] = now.Add(openAITeamLinkedErrorDedupTTL)
	return true
}

// TeamLinkedStore 保留原平台列表过滤与逐账号独立写入。
type TeamLinkedStore interface {
	ListByPlatform(context.Context, string) ([]Record, error)
	SetError(context.Context, int64, string) error
}

// TeamLinkedOptions 由组合根绑定运行阻断和日志，不持有旧网关服务。
type TeamLinkedOptions struct {
	Now   func() time.Time
	Warn  func(string, ...any)
	Block func(*Record, time.Time, string)
}

// TeamLinkedHealth 独占工作区联动的原进程内去重状态，构造不启动后台任务。
type TeamLinkedHealth struct {
	accountRepo            TeamLinkedStore
	options                TeamLinkedOptions
	openaiTeamLinkedMu     sync.Mutex
	openaiTeamLinkedRecent map[string]time.Time
}

func NewTeamLinkedHealth(store TeamLinkedStore, options TeamLinkedOptions) *TeamLinkedHealth {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if options.Block == nil {
		options.Block = func(*Record, time.Time, string) {}
	}
	return &TeamLinkedHealth{accountRepo: store, options: options}
}
