package searchtools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
)

// Searcher 由 search 的唯一 Manager 实现，配额预占和回滚不在网关重复实现。
type Searcher interface {
	SearchWithBestProvider(context.Context, contract.SearchRequest) (*contract.SearchResponse, string, error)
}
type (
	SearchSource interface{ Current() Searcher }
	Settings     interface{ IsWebSearchEmulationEnabled(context.Context) bool }
	GroupPolicy  interface {
		Enabled(context.Context, int64, string) (bool, error)
	}
)

type PolicyInput struct {
	Body           []byte
	Mode, Platform string
	GroupID        *int64
}
type Request struct {
	Body                         []byte
	AccountID                    int64
	AccountName, ProxyURL, Model string
	Stream                       bool
	OnAccepted                   func()
}

// Result 的账单 usage 保持零值；合成正文中展示的估算数不能变成供应商计量。
type Result struct {
	Model    string
	Duration time.Duration
	Usage    protocol.TokenUsage
}
type Event struct {
	Kind, Query, Provider, AccountName string
	AccountID                          int64
	Results                            int
	Err                                error
}
type Output interface {
	StartStream()
	WriteEvent(string, []byte) error
	WriteJSON([]byte)
	Flush()
}
type ProxyFailure struct{ Cause error }

func (e *ProxyFailure) Error() string { return e.Cause.Error() }
func (e *ProxyFailure) Unwrap() error { return e.Cause }

type Emulator struct {
	source        SearchSource
	settings      Settings
	groupPolicies GroupPolicy
	now           func() time.Time
	newID         func() string
	observer      func(Event)
}

// NewEmulator 不启动 worker，不复制搜索 Manager、客户端或配额状态。
func NewEmulator(source SearchSource, settings Settings, groupPolicies GroupPolicy, now func() time.Time, newID func() string, observe func(Event)) *Emulator {
	if now == nil {
		now = time.Now
	}
	return &Emulator{source: source, settings: settings, groupPolicies: groupPolicies, now: now, newID: newID, observer: observe}
}

func (s *Emulator) observe(e Event) {
	if s.observer != nil {
		s.observer(e)
	}
}

// ShouldEmulate 按 Manager、工具形状、全局、账号和分组策略的顺序短路判断。
func (s *Emulator) ShouldEmulate(ctx context.Context, in PolicyInput) bool {
	if s.source.Current() == nil {
		return false
	}
	if !IsOnlyWebSearchToolInBody(in.Body) {
		return false
	}
	if !s.settings.IsWebSearchEmulationEnabled(ctx) {
		return false
	}
	switch in.Mode {
	case "enabled":
		return true
	case "disabled":
		return false
	default:
		if in.GroupID == nil || s.groupPolicies == nil {
			return false
		}
		enabled, err := s.groupPolicies.Enabled(ctx, *in.GroupID, in.Platform)
		return err == nil && enabled
	}
}

func (s *Emulator) Search(ctx context.Context, proxyURL, query string) (*contract.SearchResponse, string, error) {
	manager := s.source.Current()
	if manager == nil {
		return nil, "", fmt.Errorf("web search emulation: manager not initialized")
	}
	response, provider, err := manager.SearchWithBestProvider(ctx, contract.SearchRequest{Query: query, MaxResults: webSearchDefaultMaxResults, ProxyURL: proxyURL})
	if err != nil {
		s.observe(Event{Kind: "search_failed", Err: err})
		return nil, "", fmt.Errorf("web search emulation: %w", err)
	}
	return response, provider, nil
}

// Execute 在本地搜索前释放串行资源；写失败停止合成输出，保留原零用量完成结果。
func (s *Emulator) Execute(ctx context.Context, in Request, out Output) (*Result, error) {
	started := s.now()
	if in.OnAccepted != nil {
		in.OnAccepted()
	}
	query := ExtractSearchQueryFromBody(in.Body)
	if query == "" {
		return nil, fmt.Errorf("web search emulation: no query found in messages")
	}
	s.observe(Event{Kind: "executing", AccountID: in.AccountID, AccountName: in.AccountName, Query: query})
	response, provider, err := s.Search(ctx, in.ProxyURL, query)
	if err != nil {
		if errors.Is(err, search.ErrProxyUnavailable) {
			return nil, &ProxyFailure{Cause: err}
		}
		return nil, err
	}
	s.observe(Event{Kind: "completed", Provider: provider, Results: len(response.Results)})
	model := in.Model
	if model == "" {
		model = defaultWebSearchModel
	}
	if in.Stream {
		return s.writeStream(out, query, response, model, started)
	}
	return s.writeJSON(out, query, response, model, started)
}
