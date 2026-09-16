package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"

	"github.com/gin-gonic/gin"
)

// ErrQoderRefreshInProgress 表示仍有其他 worker 持有刷新锁，
// 且数据库里还没有出现轮换后的凭据。
var ErrQoderRefreshInProgress = errors.New("qoder refresh in progress")

type qoderStreamClient interface {
	StreamRequestContext(ctx context.Context, session *qoder.SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error)
}

type qoderStreamClientWithDoer interface {
	StreamRequestContextWithDoer(ctx context.Context, session *qoder.SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string, doer qoder.RequestDoer) (*http.Response, error)
}

// QoderGatewayService 将 OpenAI/Anthropic 兼容请求转发到 Qoder COSY。
type QoderGatewayService struct {
	attemptActivity     func() (func(), error)
	tokenProvider       *QoderTokenProvider
	client              qoderStreamClient
	accountRepo         AccountRepository
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
	refreshAPI          *OAuthRefreshAPI
	newRefresher        func() *QoderTokenRefresher
	executor            *qoder.Executor
	conversationMu      sync.Mutex
	conversations       *qoderConversationStore
}

func NewQoderGatewayService(tokenProvider *QoderTokenProvider, accountRepo AccountRepository, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService, refreshAPI *OAuthRefreshAPI) *QoderGatewayService {
	if tokenProvider == nil {
		tokenProvider = NewQoderTokenProvider()
	}
	tokenProvider.SetHTTPUpstream(httpUpstream, tlsFPProfileService)
	return &QoderGatewayService{
		tokenProvider:       tokenProvider,
		client:              qoder.NewClient(qoder.APIBaseURL),
		accountRepo:         accountRepo,
		httpUpstream:        httpUpstream,
		tlsFPProfileService: tlsFPProfileService,
		refreshAPI:          refreshAPI,
		conversations:       newQoderConversationStore(qoderConversationTTL),
	}
}

func (s *QoderGatewayService) ForwardChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte, responseModels ...string) (*ForwardResult, error) {
	return s.executeQoder(ctx, c, account, body, protocolcore.ProtocolOpenAIChatCompletions, responseModels...)
}

func (s *QoderGatewayService) ForwardResponses(ctx context.Context, c *gin.Context, account *Account, body []byte, responseModels ...string) (*ForwardResult, error) {
	return s.executeQoder(ctx, c, account, body, protocolcore.ProtocolOpenAIResponses, responseModels...)
}

func (s *QoderGatewayService) ForwardMessages(ctx context.Context, c *gin.Context, account *Account, body []byte, responseModels ...string) (*ForwardResult, error) {
	return s.executeQoder(ctx, c, account, body, protocolcore.ProtocolAnthropicMessages, responseModels...)
}

func qoderAccountStateUpdateContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, qoderAccountStateUpdateTimeout)
}

func (s *QoderGatewayService) qoderRequestDoer(account *Account) qoder.RequestDoer {
	if s == nil {
		return nil
	}
	return newQoderRequestDoer(account, s.httpUpstream, s.tlsFPProfileService)
}

func (s *QoderGatewayService) RefreshAccountSession(ctx context.Context, account *Account) (*Account, error) {
	if s == nil {
		return nil, errors.New("qoder gateway service is not configured")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if s.accountRepo == nil {
		return nil, errors.New("qoder account repository is not configured")
	}
	refresherFactory := s.newRefresher
	if refresherFactory == nil {
		refresherFactory = func() *QoderTokenRefresher {
			return NewQoderTokenRefresherWithHTTPUpstream(nil, s.httpUpstream, s.tlsFPProfileService)
		}
	}
	refresher := refresherFactory()
	if refresher == nil {
		return nil, errors.New("qoder token refresher is nil")
	}
	refreshAPI := s.refreshAPI
	if refreshAPI == nil {
		return nil, errors.New("qoder refresh API is not configured")
	}
	failedCredentialsHash := qoderRefreshCredentialsHash(account.Credentials)
	executor := qoderGatewayRefreshExecutor{
		QoderTokenRefresher: refresher,
		failedCredentials:   failedCredentialsHash,
	}
	result, err := refreshAPI.RefreshIfNeeded(ctx, account, executor, 15*time.Minute)
	if err != nil {
		return nil, err
	}

	// 如果另一个 worker 正在刷新（LockHeld=true），等待 DB 中出现已轮换凭证。
	// 不能只 sleep 后返回当前账号：锁持有者可能尚未写回新 token，
	// handler 随后会用同一份 stale credentials 立即重试并再次 401。
	if result != nil && result.LockHeld {
		return s.waitForQoderLockedRefresh(ctx, account, failedCredentialsHash)
	}

	if result != nil && result.Account != nil {
		if s.tokenProvider != nil {
			s.tokenProvider.InvalidateAccount(result.Account)
		}
		return result.Account, nil
	}
	if s.accountRepo != nil {
		if fresh, err := s.accountRepo.GetByID(ctx, account.ID); err == nil && fresh != nil {
			if s.tokenProvider != nil {
				s.tokenProvider.InvalidateAccount(fresh)
			}
			return fresh, nil
		}
	}
	if s.tokenProvider != nil && (result == nil || result.Refreshed) {
		s.tokenProvider.Invalidate(account.ID)
	}
	return account, nil
}

func (s *QoderGatewayService) waitForQoderLockedRefresh(ctx context.Context, account *Account, failedCredentialsHash string) (*Account, error) {
	if s == nil || s.accountRepo == nil || account == nil {
		return nil, ErrQoderRefreshInProgress
	}
	waitCtx, cancel := context.WithTimeout(ctx, qoderRefreshLockWait)
	defer cancel()

	readFresh := func() (*Account, bool, error) {
		fresh, err := s.accountRepo.GetByID(waitCtx, account.ID)
		if err != nil {
			return nil, false, err
		}
		if fresh == nil {
			return nil, false, nil
		}
		if qoderRefreshCredentialsHash(fresh.Credentials) != failedCredentialsHash {
			if s.tokenProvider != nil {
				s.tokenProvider.InvalidateAccount(fresh)
			}
			return fresh, true, nil
		}
		return fresh, false, nil
	}

	var lastErr error
	if fresh, changed, err := readFresh(); changed {
		return fresh, nil
	} else if err != nil {
		lastErr = err
	}

	ticker := time.NewTicker(qoderRefreshLockPoll)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrQoderRefreshInProgress, lastErr)
			}
			return nil, fmt.Errorf("%w: %v", ErrQoderRefreshInProgress, waitCtx.Err())
		case <-ticker.C:
			fresh, changed, err := readFresh()
			if changed {
				return fresh, nil
			}
			if err != nil {
				lastErr = err
			}
		}
	}
}

type qoderGatewayRefreshExecutor struct {
	*QoderTokenRefresher
	failedCredentials string
}

func (e qoderGatewayRefreshExecutor) NeedsRefresh(account *Account, ttl time.Duration) bool {
	if e.QoderTokenRefresher == nil {
		return false
	}
	// 先检查是否能刷新，再检查是否需要刷新
	if !e.CanRefresh(account) {
		return false
	}
	if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
		return false
	}
	// request-time 401/403 刷新应基于“失败时的凭证快照”判定：
	// - DB 中凭证已经变了，说明其它 worker 已刷新，当前请求不应再次消费 refresh_token。
	// - DB 中仍是同一份失败凭证，则即使 expires_at 还没临近，也需要刷新这份已被上游拒绝的 token。
	if e.failedCredentials != "" {
		return qoderRefreshCredentialsHash(account.Credentials) == e.failedCredentials
	}
	return e.QoderTokenRefresher.NeedsRefresh(account, ttl)
}

func newQoderRequestDoer(account *Account, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) qoder.RequestDoer {
	if httpUpstream == nil || account == nil {
		return nil
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var tlsProfile *tlsfingerprint.Profile
	if tlsFPProfileService != nil {
		tlsProfile = tlsFPProfileService.ResolveTLSProfile(account)
	}
	return func(req *http.Request) (*http.Response, error) {
		return httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	}
}

func (s *QoderGatewayService) applyUpstreamErrorPolicy(ctx context.Context, account *Account, err error) {
	if s == nil || s.accountRepo == nil || account == nil || err == nil {
		return
	}
	var apiErr *qoder.APIError
	if !errors.As(err, &apiErr) {
		return
	}
	stateCtx, cancel := qoderAccountStateUpdateContext(ctx)
	defer cancel()
	switch {
	case apiErr.IsAgentLimit():
		resetAt, ok := apiErr.AgentLimitResetAt()
		if !ok {
			resetAt = time.Now().Add(30 * time.Second)
		}
		_ = s.accountRepo.SetRateLimited(stateCtx, account.ID, resetAt)
	case apiErr.StatusCode == http.StatusTooManyRequests:
		_ = s.accountRepo.SetRateLimited(stateCtx, account.ID, time.Now().Add(30*time.Second))
	case apiErr.StatusCode >= 500:
		_ = s.accountRepo.SetOverloaded(stateCtx, account.ID, time.Now().Add(30*time.Second))
	}
}

func applyQoderAccountModelMapping(account *Account, body []byte) []byte {
	if account == nil || !account.IsQoder() || len(body) == 0 {
		return body
	}
	requestModel := strings.TrimSpace(gjsonString(body, "model"))
	if requestModel == "" {
		return body
	}
	mappedModel, matched := account.ResolveMappedModel(requestModel)
	if !matched || mappedModel == "" || mappedModel == requestModel {
		return body
	}
	return ReplaceModelInBody(body, mappedModel)
}

func qoderUserType(account *Account) string {
	if account == nil {
		return "personal_standard"
	}
	return firstNonEmptyQoder(account.GetCredential("user_type"), "personal_standard")
}

// qoderExecutor 只装配唯一平台会话实例，构造期间不启动后台任务。
func (s *QoderGatewayService) qoderExecutor() *qoder.Executor {
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	if s.conversations == nil {
		s.conversations = newQoderConversationStore(qoderConversationTTL)
	}
	if s.executor == nil {
		s.executor = qoder.NewExecutor(qoder.ExecuteOptions{Conversations: s.conversations, Enter: s.attemptActivity})
	}
	return s.executor
}

// qoderTarget 只把现有凭据和传输端口转换为一次调用的受控句柄。
func (s *QoderGatewayService) qoderTarget(c *gin.Context, account *Account) *qoder.Target {
	site, err := qoderSiteForAccount(account)
	if err != nil {
		site = qoder.SiteGlobal
	}
	return &qoder.Target{AccountID: qoderAccountID(account), Site: site, UserType: qoderUserType(account), Metadata: qoderRequestMetadata(c),
		Session: func(ctx context.Context) (*qoder.SessionContext, error) {
			return s.tokenProvider.GetSession(ctx, account)
		},
		Client: func() (qoder.StreamClient, error) { return qoderStreamClientForAccount(s.client, account) }, Doer: s.qoderRequestDoer(account)}
}

// executeQoder 保留旧错误副作用和结果形状，实际平台执行与转换只存在于 upstream/qoder。
func (s *QoderGatewayService) executeQoder(ctx context.Context, c *gin.Context, account *Account, body []byte, wire protocolcore.ProtocolID, responseModels ...string) (*ForwardResult, error) {
	responseModel := firstNonEmptyQoder(responseModels...)
	if responseModel == "" {
		responseModel = strings.TrimSpace(gjsonString(body, "model"))
	}
	input := upstream.AttemptInput{Protocol: wire, ResponseModel: responseModel, Stream: gjsonBool(body, "stream"), Body: applyQoderAccountModelMapping(account, body), Target: s.qoderTarget(c, account)}
	result, err := s.qoderExecutor().Execute(ctx, input, gatewayhttp.ResponseSink{Writer: c.Writer})
	if err != nil {
		s.applyUpstreamErrorPolicy(ctx, account, err)
		if !result.Served || !result.HasUsage {
			return nil, err
		}
	}
	return &ForwardResult{RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, Usage: result.Usage, Stream: result.Stream, Duration: result.Duration, ClientDisconnect: result.ClientDisconnect}, err
}

// PrepareQoderAttempt 为新网关提供已投影的单次执行，不向核心暴露旧账号。
func (s *QoderGatewayService) PrepareQoderAttempt(c *gin.Context, account *Account, body []byte, wire protocolcore.ProtocolID, responseModel string) (upstream.Executor, upstream.AttemptInput) {
	return s.qoderExecutor(), upstream.AttemptInput{Protocol: wire, Body: applyQoderAccountModelMapping(account, body), ResponseModel: responseModel, Stream: gjsonBool(body, "stream"), Target: s.qoderTarget(c, account)}
}

// ObserveQoderFailure 只转交原账号后置处理，资金与重试不在此执行。
func (s *QoderGatewayService) ObserveQoderFailure(ctx context.Context, account *Account, err error) {
	s.applyUpstreamErrorPolicy(ctx, account, err)
}

// ForwardResultFromAttempt 保留完成 worker 的过渡数据形状。
func ForwardResultFromAttempt(result upstream.AttemptResult) *ForwardResult {
	return &ForwardResult{RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, Usage: result.Usage, Stream: result.Stream, Duration: result.Duration, ClientDisconnect: result.ClientDisconnect, FirstTokenMs: result.FirstTokenMs}
}

// BindAttemptActivity 由 app 在开放入口前绑定同步执行拥有者，平台不会引用生命周期包。
func (s *QoderGatewayService) BindAttemptActivity(enter func() (func(), error)) {
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	s.attemptActivity = enter
}
