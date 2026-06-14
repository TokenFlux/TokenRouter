package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	qoderDefaultMaxTokens = 32768
	qoderStreamTimeout    = 15 * time.Minute
	qoderKeepaliveEvery   = 10 * time.Second
	qoderConversationTTL  = 2 * time.Hour
)

var qoderClaudeBillingCCHRe = regexp.MustCompile(`(x-anthropic-billing-header:[^\n\r;]*?(?:;[^\n\r;]*?)*\bcch=)[0-9a-fA-F]{5}(;)`)

// defaultQoderModelAliases maps TokenRouter request-side aliases to Qoder API
// keys. Keep this public surface small: raw Qoder route keys stay internal
// unless they are intentionally exposed as readable request aliases.
var defaultQoderModelAliases = map[string]qoderModelInfo{
	// Confirmed Claude Opus 4.6 via encrypted reasoning metadata.
	"claude-opus-4-6": {Key: "ultimate", Source: "system", Provider: "Claude", Notes: "Confirmed Claude Opus 4.6 via encrypted reasoning metadata.", DisplayName: "Claude Opus 4.6"},
	// Qoder-selected route; exact upstream model is dynamic and unconfirmed.
	"auto": {Key: "auto", Source: "system", Provider: "Qoder", Notes: "Qoder-selected route; exact upstream model is dynamic and unconfirmed.", DisplayName: "Qoder Auto"},
	// Qoder performance/efficient/lite tiers do not have a confirmed concrete provider model.
	"performance": {Key: "performance", Source: "system", Provider: "Qoder", Notes: "Qoder performance tier; exact upstream model is unconfirmed.", DisplayName: "Qoder Performance"},
	"efficient":   {Key: "efficient", Source: "system", Provider: "Qoder", Notes: "Qoder efficient tier; exact upstream model is unconfirmed.", DisplayName: "Qoder Efficient"},
	// Unverified Qoder lite tier; observations are mixed.
	"lite": {Key: "lite", Source: "system", Provider: "Qoder", Notes: "Unverified Qoder lite tier; observations are mixed.", DisplayName: "Qoder Lite"},
	// Qoder UI exposes these provider model names; map readable public aliases to internal Qoder route keys.
	"qwen3.7-max":       {Key: "qmodel_latest", Source: "system", Provider: "Qwen", Notes: "Qoder UI model name Qwen3.7-Max.", DisplayName: "Qwen3.7-Max"},
	"qwen3.7-plus":      {Key: "qmodel", Source: "system", Provider: "Qwen", Notes: "Qoder UI model name Qwen3.7-Plus.", DisplayName: "Qwen3.7-Plus"},
	"qwen3.5-plus":      {Key: "q35model", Source: "system", Provider: "Qwen", Notes: "Qoder UI model name Qwen3.5-Plus.", DisplayName: "Qwen3.5-Plus"},
	"deepseek-v4-pro":   {Key: "dmodel", Source: "system", Provider: "DeepSeek", Notes: "Qoder UI model name DeepSeek-V4-Pro.", DisplayName: "DeepSeek-V4-Pro"},
	"deepseek-v4-flash": {Key: "dfmodel", Source: "system", Provider: "DeepSeek", Notes: "Qoder UI model name DeepSeek-V4-Flash.", DisplayName: "DeepSeek-V4-Flash"},
	"glm-5":             {Key: "gmodel", Source: "system", Provider: "GLM", Notes: "Qoder UI model name GLM-5.", DisplayName: "GLM-5"},
	"glm-5.1":           {Key: "gm51model", Source: "system", Provider: "GLM", Notes: "Qoder UI model name GLM-5.1.", DisplayName: "GLM-5.1"},
	"kimi-k2.6":         {Key: "kmodel", Source: "system", Provider: "Kimi", Notes: "Qoder UI model name Kimi-K2.6.", DisplayName: "Kimi-K2.6"},
	"minimax-m3":        {Key: "mmodel", Source: "system", Provider: "MiniMax", Notes: "Qoder UI model name MiniMax-M3.", DisplayName: "MiniMax-M3"},
}

var qoderCompatModelAliases = map[string]qoderModelInfo{
	// Compatibility only: existing configs may still contain the raw Qoder key.
	"ultimate": {Key: "ultimate", Source: "system", Provider: "Claude", Notes: "Compatibility alias for Qoder ultimate; expose claude-opus-4-6 instead.", DisplayName: "Claude Opus 4.6"},
	// Compatibility only: keep resolving the old inferred Kimi label, but expose kimi-k2.6 in defaults.
	"kimi-k2.7-code": {Key: "kmodel", Source: "system", Provider: "Kimi", Notes: "Compatibility alias for Qoder Kimi route; expose kimi-k2.6 instead.", DisplayName: "Kimi-K2.6"},
}

type qoderModelInfo struct {
	Key         string `json:"key"`
	Source      string `json:"source"`
	Provider    string `json:"provider,omitempty"`
	Notes       string `json:"notes,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type qoderStreamClient interface {
	StreamRequestContext(ctx context.Context, session *qoder.SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string) (*http.Response, error)
}

type qoderStreamClientWithDoer interface {
	StreamRequestContextWithDoer(ctx context.Context, session *qoder.SessionContext, path string, bodyJSON []byte, extraHeaders map[string]string, doer qoder.RequestDoer) (*http.Response, error)
}

// QoderGatewayService forwards OpenAI/Anthropic-compatible requests to Qoder COSY.
type QoderGatewayService struct {
	tokenProvider       *QoderTokenProvider
	client              qoderStreamClient
	accountRepo         AccountRepository
	httpUpstream        HTTPUpstream
	tlsFPProfileService *TLSFingerprintProfileService
	newRefresher        func() *QoderTokenRefresher
	conversationMu      sync.Mutex
	conversations       *qoderConversationStore
}

func NewQoderGatewayService(tokenProvider *QoderTokenProvider, accountRepo AccountRepository, httpUpstream HTTPUpstream, tlsFPProfileService *TLSFingerprintProfileService) *QoderGatewayService {
	if tokenProvider == nil {
		tokenProvider = NewQoderTokenProvider()
	}
	return &QoderGatewayService{
		tokenProvider:       tokenProvider,
		client:              qoder.NewClient(qoder.APIBaseURL),
		accountRepo:         accountRepo,
		httpUpstream:        httpUpstream,
		tlsFPProfileService: tlsFPProfileService,
		conversations:       newQoderConversationStore(qoderConversationTTL),
	}
}

func (s *QoderGatewayService) ForwardChatCompletions(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	streamCtx, cancel := context.WithTimeout(ctx, qoderStreamTimeout)
	defer cancel()

	requestModel := strings.TrimSpace(gjsonString(body, "model"))
	body = applyQoderAccountModelMapping(account, body)
	payload, modelKey, conversationPlan, err := s.buildQoderPayloadFromChatCompletions(c, account, body)
	if err != nil {
		return nil, err
	}
	clientStream := gjsonBool(body, "stream")
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal qoder payload: %w", err)
	}

	resp, err := s.openQoderStream(streamCtx, account, payloadBody, modelKey)
	if err != nil {
		s.applyUpstreamErrorPolicy(ctx, account, err)
		return nil, err
	}
	// Once Qoder accepts the request, reserve the session immediately. Claude Code
	// can issue follow-up or duplicate requests before the stream fully drains.
	conversationPlan.commit()

	var responseBody []byte
	upstreamUsage := ClaudeUsage{}
	if clientStream {
		streamResult, err := WriteQoderOpenAIStreamResponse(c, requestModel, resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = streamResult.Usage
	} else {
		events, err := ReadQoderSSEEvents(resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = qoderUsageFromEvents(events)
		responseBody, err = BuildQoderOpenAICompletion(requestModel, events)
		if err != nil {
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}
	recordUsage := conversationPlan.recordUsage(upstreamUsage)
	conversationPlan.logUsage(c, account, upstreamUsage, recordUsage)
	conversationPlan.commit(upstreamUsage)

	return &ForwardResult{
		Model:         requestModel,
		UpstreamModel: modelKey,
		Usage:         recordUsage,
		Stream:        clientStream,
		Duration:      time.Since(start),
		ResponseBody:  responseBody,
	}, nil
}

func (s *QoderGatewayService) ForwardMessages(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	streamCtx, cancel := context.WithTimeout(ctx, qoderStreamTimeout)
	defer cancel()

	requestModel := strings.TrimSpace(gjsonString(body, "model"))
	body = applyQoderAccountModelMapping(account, body)
	payload, modelKey, conversationPlan, err := s.buildQoderPayloadFromAnthropicMessages(c, account, body)
	if err != nil {
		return nil, err
	}
	clientStream := gjsonBool(body, "stream")
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal qoder payload: %w", err)
	}

	resp, err := s.openQoderStream(streamCtx, account, payloadBody, modelKey)
	if err != nil {
		s.applyUpstreamErrorPolicy(ctx, account, err)
		return nil, err
	}
	// Once Qoder accepts the request, reserve the session immediately. Claude Code
	// can issue follow-up or duplicate requests before the stream fully drains.
	conversationPlan.commit()

	var responseBody []byte
	upstreamUsage := ClaudeUsage{}
	if clientStream {
		streamResult, err := WriteQoderAnthropicStreamResponse(c, requestModel, resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = streamResult.Usage
	} else {
		events, err := ReadQoderSSEEvents(resp)
		if err != nil {
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = qoderUsageFromEvents(events)
		responseBody, err = BuildQoderAnthropicMessage(requestModel, events)
		if err != nil {
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}
	recordUsage := conversationPlan.recordUsage(upstreamUsage)
	conversationPlan.logUsage(c, account, upstreamUsage, recordUsage)
	conversationPlan.commit(upstreamUsage)

	return &ForwardResult{
		Model:         requestModel,
		UpstreamModel: modelKey,
		Usage:         recordUsage,
		Stream:        clientStream,
		Duration:      time.Since(start),
		ResponseBody:  responseBody,
	}, nil
}

type qoderPayloadBuildResult struct {
	Payload  map[string]any
	ModelKey string
	Plan     *qoderConversationPlan
}

type qoderPayloadRequest struct {
	model           string
	system          string
	messages        []qoderMessage
	tools           []any
	maxTokens       int
	userType        string
	explicitSession string
	promptCacheKey  string
	metadataUserID  string
}

func (s *QoderGatewayService) buildQoderPayloadFromChatCompletions(c *gin.Context, account *Account, body []byte) (map[string]any, string, *qoderConversationPlan, error) {
	request, err := parseQoderChatCompletionsPayload(body)
	if err != nil {
		return nil, "", nil, err
	}
	result := s.buildQoderPayloadWithConversation(c, account, "openai_chat_completions", request)
	return result.Payload, result.ModelKey, result.Plan, nil
}

func (s *QoderGatewayService) buildQoderPayloadFromAnthropicMessages(c *gin.Context, account *Account, body []byte) (map[string]any, string, *qoderConversationPlan, error) {
	request, err := parseQoderAnthropicMessagesPayload(body)
	if err != nil {
		return nil, "", nil, err
	}
	result := s.buildQoderPayloadWithConversation(c, account, "anthropic_messages", request)
	return result.Payload, result.ModelKey, result.Plan, nil
}

func (s *QoderGatewayService) buildQoderPayloadWithConversation(c *gin.Context, account *Account, protocol string, request qoderPayloadRequest) qoderPayloadBuildResult {
	store := s.qoderConversationStore()
	request.userType = qoderUserType(account)
	key, keySource := qoderConversationKey(c, account, protocol, request)
	plan := store.plan(key, request.system, request.tools, request.messages)
	payload, modelKey := buildQoderPayloadWithOptions(request, plan.sessionID, plan.messagesToSend, plan.includeSystem, plan.includeTools)
	plan.log(c, account, protocol, request.model, keySource, request, payload)
	return qoderPayloadBuildResult{Payload: payload, ModelKey: modelKey, Plan: plan}
}

func (s *QoderGatewayService) qoderConversationStore() *qoderConversationStore {
	if s == nil {
		return newQoderConversationStore(qoderConversationTTL)
	}
	s.conversationMu.Lock()
	defer s.conversationMu.Unlock()
	if s.conversations == nil {
		s.conversations = newQoderConversationStore(qoderConversationTTL)
	}
	return s.conversations
}

func (s *QoderGatewayService) openQoderStream(ctx context.Context, account *Account, payload []byte, modelKey string) (*http.Response, error) {
	if s == nil || s.tokenProvider == nil {
		return nil, errors.New("qoder gateway service is not configured")
	}
	session, err := s.tokenProvider.GetSession(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get qoder session: %w", err)
	}
	client := s.client
	if client == nil {
		client = qoder.NewClient(qoder.APIBaseURL)
	}

	headers := map[string]string{
		"x-model-key":    modelKey,
		"x-model-source": "system",
	}
	if doer := s.qoderRequestDoer(account); doer != nil {
		if doerClient, ok := client.(qoderStreamClientWithDoer); ok {
			return doerClient.StreamRequestContextWithDoer(ctx, session, "", payload, headers, doer)
		}
	}
	return client.StreamRequestContext(ctx, session, "", payload, headers)
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
	refresherFactory := s.newRefresher
	if refresherFactory == nil {
		refresherFactory = func() *QoderTokenRefresher {
			return NewQoderTokenRefresher(nil)
		}
	}
	refresher := refresherFactory()
	if refresher == nil {
		return nil, errors.New("qoder token refresher is nil")
	}
	newCredentials, err := refresher.Refresh(ctx, account)
	if err != nil {
		return nil, err
	}
	if newCredentials == nil {
		newCredentials = map[string]any{}
	}
	newCredentials["_token_version"] = time.Now().UnixMilli()
	if err := persistAccountCredentials(ctx, s.accountRepo, account, newCredentials); err != nil {
		return nil, fmt.Errorf("save qoder credentials: %w", err)
	}
	if s.tokenProvider != nil {
		s.tokenProvider.Invalidate(account.ID)
	}
	if s.accountRepo != nil {
		if fresh, err := s.accountRepo.GetByID(ctx, account.ID); err == nil && fresh != nil {
			return fresh, nil
		}
	}
	return account, nil
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
	switch {
	case apiErr.IsAgentLimit():
		resetAt, ok := apiErr.AgentLimitResetAt()
		if !ok {
			resetAt = time.Now().Add(30 * time.Second)
		}
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, resetAt)
	case apiErr.StatusCode == http.StatusTooManyRequests:
		_ = s.accountRepo.SetRateLimited(ctx, account.ID, time.Now().Add(30*time.Second))
	case apiErr.StatusCode >= 500:
		_ = s.accountRepo.SetOverloaded(ctx, account.ID, time.Now().Add(30*time.Second))
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

func BuildQoderPayloadFromChatCompletions(body []byte, userType string) (map[string]any, string, error) {
	request, err := parseQoderChatCompletionsPayload(body)
	if err != nil {
		return nil, "", err
	}
	request.userType = userType
	payload, modelKey := buildQoderPayloadWithOptions(request, "", request.messages, true, true)
	return payload, modelKey, nil
}

func parseQoderChatCompletionsPayload(body []byte) (qoderPayloadRequest, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return qoderPayloadRequest{}, fmt.Errorf("parse chat completions request: %w", err)
	}
	model, _ := req["model"].(string)
	if strings.TrimSpace(model) == "" {
		return qoderPayloadRequest{}, errors.New("model is required")
	}

	var messages []qoderMessage
	var systemParts []string
	rawMessages, _ := req["messages"].([]any)
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		text := openAIMessageText(msg)
		if role == "system" {
			if text != "" {
				systemParts = append(systemParts, text)
			}
			continue
		}
		messages = append(messages, qoderMessage{Role: role, Text: text, Raw: msg})
	}

	tools := qoderAnySlice(req["tools"])
	maxTokens := numberAsInt(req["max_tokens"], qoderDefaultMaxTokens)
	return qoderPayloadRequest{
		model:           model,
		system:          strings.Join(systemParts, "\n"),
		messages:        messages,
		tools:           tools,
		maxTokens:       maxTokens,
		explicitSession: firstNonEmptyQoder(qoderStringField(req, "session_id"), qoderStringField(req, "conversation_id")),
		promptCacheKey:  qoderStringField(req, "prompt_cache_key"),
		metadataUserID:  qoderMetadataUserID(req["metadata"]),
	}, nil
}

func BuildQoderPayloadFromAnthropicMessages(body []byte, userType string) (map[string]any, string, error) {
	request, err := parseQoderAnthropicMessagesPayload(body)
	if err != nil {
		return nil, "", err
	}
	request.userType = userType
	payload, modelKey := buildQoderPayloadWithOptions(request, "", request.messages, true, true)
	return payload, modelKey, nil
}

func parseQoderAnthropicMessagesPayload(body []byte) (qoderPayloadRequest, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return qoderPayloadRequest{}, fmt.Errorf("parse anthropic messages request: %w", err)
	}
	model, _ := req["model"].(string)
	if strings.TrimSpace(model) == "" {
		return qoderPayloadRequest{}, errors.New("model is required")
	}

	system, err := anthropicSystemText(req["system"])
	if err != nil {
		return qoderPayloadRequest{}, err
	}

	var messages []qoderMessage
	rawMessages, _ := req["messages"].([]any)
	for _, raw := range rawMessages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		converted, err := anthropicMessageToQoderMessages(msg)
		if err != nil {
			return qoderPayloadRequest{}, err
		}
		messages = append(messages, converted...)
	}

	tools := convertAnthropicToolsToQoderTools(req["tools"])
	maxTokens := numberAsInt(req["max_tokens"], qoderDefaultMaxTokens)
	return qoderPayloadRequest{
		model:           model,
		system:          system,
		messages:        messages,
		tools:           tools,
		maxTokens:       maxTokens,
		explicitSession: firstNonEmptyQoder(qoderStringField(req, "session_id"), qoderStringField(req, "conversation_id")),
		promptCacheKey:  qoderStringField(req, "prompt_cache_key"),
		metadataUserID:  qoderMetadataUserID(req["metadata"]),
	}, nil
}

type qoderMessage struct {
	Role       string
	Text       string
	ToolCallID string
	Raw        map[string]any
}

type qoderConversationStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[string]*qoderConversationState
}

type qoderConversationState struct {
	sessionID           string
	systemFingerprint   string
	toolsFingerprint    string
	messageFingerprints []string
	hasUsage            bool
	lastUsageInput      int
	lastUsageOutput     int
	expiresAt           time.Time
}

type qoderConversationPlan struct {
	store                 *qoderConversationStore
	key                   string
	sessionID             string
	messagesToSend        []qoderMessage
	includeSystem         bool
	includeTools          bool
	reused                bool
	fallback              bool
	systemFingerprint     string
	toolsFingerprint      string
	messageFingerprints   []string
	committedFingerprints []string
	hasPreviousUsage      bool
	previousUsageInput    int
	previousUsageOutput   int
	matchStatus           string
	previousMessageCount  int
	prefixMessageCount    int
	storeItemCount        int
	previousFirstHash     string
	currentFirstHash      string
	diagnostics           qoderConversationDiagnostics
}

type qoderConversationDiagnostics struct {
	protocol             string
	model                string
	keySource            string
	requestID            string
	originalMessages     int
	sentMessages         int
	originalToolsCount   int
	sentToolsCount       int
	originalToolsBytes   int
	sentToolsBytes       int
	systemBytes          int
	sentSystemBytes      int
	outboundPayloadBytes int
}

func newQoderConversationStore(ttl time.Duration) *qoderConversationStore {
	if ttl <= 0 {
		ttl = qoderConversationTTL
	}
	return &qoderConversationStore{
		ttl:   ttl,
		items: make(map[string]*qoderConversationState),
	}
}

func (s *qoderConversationStore) plan(key, system string, tools []any, messages []qoderMessage) *qoderConversationPlan {
	systemFingerprint := qoderSystemFingerprint(system)
	toolsFingerprint := qoderFingerprintAny(tools)
	messageFingerprints := qoderMessageFingerprints(messages)
	currentFirstHash := firstQoderFingerprint(messageFingerprints)
	fullPlan := func(fallback bool, matchStatus string, state *qoderConversationState, storeItemCount int) *qoderConversationPlan {
		previousMessageCount := 0
		previousFirstHash := ""
		if state != nil {
			previousMessageCount = len(state.messageFingerprints)
			previousFirstHash = firstQoderFingerprint(state.messageFingerprints)
		}
		return &qoderConversationPlan{
			store:                 s,
			key:                   key,
			sessionID:             qoderSessionIDForConversation(key, systemFingerprint, toolsFingerprint, messageFingerprints),
			messagesToSend:        messages,
			includeSystem:         true,
			includeTools:          true,
			fallback:              fallback,
			systemFingerprint:     systemFingerprint,
			toolsFingerprint:      toolsFingerprint,
			messageFingerprints:   messageFingerprints,
			committedFingerprints: messageFingerprints,
			matchStatus:           matchStatus,
			previousMessageCount:  previousMessageCount,
			storeItemCount:        storeItemCount,
			previousFirstHash:     previousFirstHash,
			currentFirstHash:      currentFirstHash,
		}
	}
	if s == nil || strings.TrimSpace(key) == "" {
		plan := fullPlan(true, "disabled", nil, 0)
		plan.store = nil
		plan.key = ""
		plan.sessionID = uuid.NewString()
		return plan
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = make(map[string]*qoderConversationState)
	}
	s.pruneExpiredLocked(now)
	storeItemCount := len(s.items)
	state := s.items[key]
	if state == nil {
		return fullPlan(false, "no_state", nil, storeItemCount)
	}
	if now.After(state.expiresAt) {
		delete(s.items, key)
		return fullPlan(true, "expired", state, storeItemCount)
	}
	if state.systemFingerprint != systemFingerprint {
		return fullPlan(true, "system_mismatch", state, storeItemCount)
	}
	if state.toolsFingerprint != toolsFingerprint {
		return fullPlan(true, "tools_mismatch", state, storeItemCount)
	}
	prefixLen, ok := qoderConversationPrefixLen(state.messageFingerprints, messageFingerprints)
	if !ok {
		return fullPlan(true, "prefix_mismatch", state, storeItemCount)
	}
	return &qoderConversationPlan{
		store:                 s,
		key:                   key,
		sessionID:             state.sessionID,
		messagesToSend:        messages[prefixLen:],
		includeSystem:         false,
		includeTools:          false,
		reused:                true,
		systemFingerprint:     systemFingerprint,
		toolsFingerprint:      toolsFingerprint,
		messageFingerprints:   messageFingerprints,
		committedFingerprints: messageFingerprints,
		hasPreviousUsage:      state.hasUsage,
		previousUsageInput:    state.lastUsageInput,
		previousUsageOutput:   state.lastUsageOutput,
		matchStatus:           "reused",
		previousMessageCount:  len(state.messageFingerprints),
		prefixMessageCount:    prefixLen,
		storeItemCount:        storeItemCount,
		previousFirstHash:     firstQoderFingerprint(state.messageFingerprints),
		currentFirstHash:      currentFirstHash,
	}
}

func (s *qoderConversationStore) pruneExpiredLocked(now time.Time) {
	if s == nil || len(s.items) == 0 {
		return
	}
	for key, state := range s.items {
		if state == nil || now.After(state.expiresAt) {
			delete(s.items, key)
		}
	}
}

func (p *qoderConversationPlan) commit(usages ...ClaudeUsage) {
	if p == nil || p.store == nil || strings.TrimSpace(p.key) == "" {
		return
	}
	hasUsage := p.hasPreviousUsage
	lastUsageInput := p.previousUsageInput
	lastUsageOutput := p.previousUsageOutput
	if len(usages) > 0 && (usages[0].InputTokens > 0 || usages[0].OutputTokens > 0) {
		hasUsage = true
		lastUsageInput = usages[0].InputTokens
		lastUsageOutput = usages[0].OutputTokens
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if p.store.items == nil {
		p.store.items = make(map[string]*qoderConversationState)
	}
	committedFingerprints := append([]string(nil), p.committedFingerprints...)
	if existing := p.store.items[p.key]; existing != nil &&
		existing.sessionID == p.sessionID &&
		existing.systemFingerprint == p.systemFingerprint &&
		existing.toolsFingerprint == p.toolsFingerprint {
		if _, ok := qoderConversationPrefixLen(committedFingerprints, existing.messageFingerprints); ok && len(existing.messageFingerprints) > len(committedFingerprints) {
			committedFingerprints = append([]string(nil), existing.messageFingerprints...)
		}
		if existing.hasUsage && (!hasUsage || existing.lastUsageInput > lastUsageInput || existing.lastUsageOutput > lastUsageOutput) {
			hasUsage = true
			if existing.lastUsageInput > lastUsageInput {
				lastUsageInput = existing.lastUsageInput
			}
			if existing.lastUsageOutput > lastUsageOutput {
				lastUsageOutput = existing.lastUsageOutput
			}
		}
	}
	p.store.items[p.key] = &qoderConversationState{
		sessionID:           p.sessionID,
		systemFingerprint:   p.systemFingerprint,
		toolsFingerprint:    p.toolsFingerprint,
		messageFingerprints: committedFingerprints,
		hasUsage:            hasUsage,
		lastUsageInput:      lastUsageInput,
		lastUsageOutput:     lastUsageOutput,
		expiresAt:           time.Now().Add(p.store.ttl),
	}
}

func (p *qoderConversationPlan) log(c *gin.Context, account *Account, protocol, model, keySource string, request qoderPayloadRequest, payload map[string]any) {
	if p == nil {
		return
	}
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	originalToolsBytes := qoderJSONSize(request.tools)
	sentTools := qoderAnySlice(payload["tools"])
	sentToolsBytes := qoderJSONSize(sentTools)
	systemBytes := len([]byte(request.system))
	sentSystemBytes := 0
	if p.includeSystem {
		sentSystemBytes = systemBytes
	}
	p.diagnostics = qoderConversationDiagnostics{
		protocol:             protocol,
		model:                model,
		keySource:            keySource,
		requestID:            qoderStringField(payload, "request_id"),
		originalMessages:     len(request.messages),
		sentMessages:         len(p.messagesToSend),
		originalToolsCount:   len(request.tools),
		sentToolsCount:       len(sentTools),
		originalToolsBytes:   originalToolsBytes,
		sentToolsBytes:       sentToolsBytes,
		systemBytes:          systemBytes,
		sentSystemBytes:      sentSystemBytes,
		outboundPayloadBytes: qoderJSONSize(payload),
	}
	logger.L().Info("qoder session",
		zap.String("protocol", protocol),
		zap.String("model", model),
		zap.String("key_source", keySource),
		zap.String("key_hash", hashSensitiveValueForLog(p.key)),
		zap.String("match_status", p.matchStatus),
		zap.Int64("account_id", accountID),
		zap.Int64("api_key_id", getAPIKeyIDFromContext(c)),
		zap.String("request_id", p.diagnostics.requestID),
		zap.Bool("reused", p.reused),
		zap.Bool("used_full_replay", !p.reused),
		zap.Bool("include_system", p.includeSystem),
		zap.Bool("include_tools", p.includeTools),
		zap.Int("store_item_count", p.storeItemCount),
		zap.Int("previous_messages", p.previousMessageCount),
		zap.Int("prefix_messages", p.prefixMessageCount),
		zap.Int("original_messages", p.diagnostics.originalMessages),
		zap.Int("sent_messages", p.diagnostics.sentMessages),
		zap.String("previous_first_message_hash", p.previousFirstHash),
		zap.String("current_first_message_hash", p.currentFirstHash),
		zap.Bool("first_message_hash_changed", p.previousFirstHash != "" && p.currentFirstHash != "" && p.previousFirstHash != p.currentFirstHash),
		zap.Bool("system_changed", p.matchStatus == "system_mismatch"),
		zap.Bool("tools_changed", p.matchStatus == "tools_mismatch"),
		zap.Int("original_tools_count", p.diagnostics.originalToolsCount),
		zap.Int("sent_tools_count", p.diagnostics.sentToolsCount),
		zap.Int("original_tools_bytes", p.diagnostics.originalToolsBytes),
		zap.Int("sent_tools_bytes", p.diagnostics.sentToolsBytes),
		zap.Int("system_bytes", p.diagnostics.systemBytes),
		zap.Int("sent_system_bytes", p.diagnostics.sentSystemBytes),
		zap.Int("outbound_payload_bytes", p.diagnostics.outboundPayloadBytes),
	)
}

func (p *qoderConversationPlan) recordUsage(upstreamUsage ClaudeUsage) ClaudeUsage {
	if p == nil {
		return upstreamUsage
	}
	previousInput, previousOutput, hasPrevious := p.previousUsageForRecord()
	if !p.shouldTreatUsageAsCumulative(upstreamUsage, previousInput, hasPrevious) {
		return upstreamUsage
	}
	usage := upstreamUsage
	usage.InputTokens = max(upstreamUsage.InputTokens-previousInput, 0)
	if upstreamUsage.OutputTokens >= previousOutput {
		usage.OutputTokens = upstreamUsage.OutputTokens - previousOutput
	}
	return usage
}

func (p *qoderConversationPlan) previousUsageForRecord() (int, int, bool) {
	if p == nil {
		return 0, 0, false
	}
	input := p.previousUsageInput
	output := p.previousUsageOutput
	hasPrevious := p.hasPreviousUsage
	if p.store == nil || strings.TrimSpace(p.key) == "" {
		return input, output, hasPrevious
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	state := p.store.items[p.key]
	if state == nil || !state.hasUsage || state.sessionID != p.sessionID {
		return input, output, hasPrevious
	}
	if _, ok := qoderConversationPrefixLen(state.messageFingerprints, p.messageFingerprints); !ok {
		return input, output, hasPrevious
	}
	if state.lastUsageInput > input {
		input = state.lastUsageInput
	}
	if state.lastUsageOutput > output {
		output = state.lastUsageOutput
	}
	return input, output, true
}

func (p *qoderConversationPlan) shouldTreatUsageAsCumulative(usage ClaudeUsage, previousInput int, hasPrevious bool) bool {
	if p == nil || !p.reused || !hasPrevious || previousInput <= 0 || usage.InputTokens <= previousInput {
		return false
	}
	diag := p.diagnostics
	if !(diag.sentMessages < diag.originalMessages || diag.sentToolsBytes < diag.originalToolsBytes || diag.sentSystemBytes < diag.systemBytes) {
		return false
	}
	// For per-request usage, token count should be bounded by the JSON wire size
	// with a generous margin. Cumulative Qoder counters can exceed the reduced
	// incremental payload by a wide margin after tools/system are omitted.
	return usage.InputTokens > diag.outboundPayloadBytes
}

func (p *qoderConversationPlan) logUsage(c *gin.Context, account *Account, upstreamUsage ClaudeUsage, recordUsage ClaudeUsage) {
	if p == nil {
		return
	}
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	diag := p.diagnostics
	previousInput, previousOutput, hasPreviousUsage := p.previousUsageForRecord()
	upstreamUsageCumulativeSuspected := p.shouldTreatUsageAsCumulative(upstreamUsage, previousInput, hasPreviousUsage)
	logger.L().Info("qoder usage",
		zap.String("protocol", diag.protocol),
		zap.String("model", diag.model),
		zap.String("key_source", diag.keySource),
		zap.Int64("account_id", accountID),
		zap.Int64("api_key_id", getAPIKeyIDFromContext(c)),
		zap.String("request_id", diag.requestID),
		zap.Bool("reused", p.reused),
		zap.Bool("used_full_replay", !p.reused),
		zap.Bool("include_system", p.includeSystem),
		zap.Bool("include_tools", p.includeTools),
		zap.Int("original_messages", diag.originalMessages),
		zap.Int("sent_messages", diag.sentMessages),
		zap.Int("sent_tools_bytes", diag.sentToolsBytes),
		zap.Int("sent_system_bytes", diag.sentSystemBytes),
		zap.Int("outbound_payload_bytes", diag.outboundPayloadBytes),
		zap.String("usage_source", "qoder_sse"),
		zap.Int("upstream_usage_input_tokens", upstreamUsage.InputTokens),
		zap.Int("upstream_usage_output_tokens", upstreamUsage.OutputTokens),
		zap.Int("recorded_usage_input_tokens", recordUsage.InputTokens),
		zap.Int("recorded_usage_output_tokens", recordUsage.OutputTokens),
		zap.Bool("has_previous_upstream_usage", hasPreviousUsage),
		zap.Int("previous_upstream_usage_input_tokens", previousInput),
		zap.Int("previous_upstream_usage_output_tokens", previousOutput),
		zap.Bool("wire_payload_reduced", p.reused && (diag.sentMessages < diag.originalMessages || diag.sentToolsBytes < diag.originalToolsBytes || diag.sentSystemBytes < diag.systemBytes)),
		zap.Bool("upstream_usage_cumulative_suspected", upstreamUsageCumulativeSuspected),
	)
}

func qoderConversationKey(c *gin.Context, account *Account, protocol string, request qoderPayloadRequest) (string, string) {
	apiKeyID := getAPIKeyIDFromContext(c)
	if value := strings.TrimSpace(request.explicitSession); value != "" {
		return "body_session:" + isolateOpenAISessionID(apiKeyID, value), "body_session"
	}
	if value := strings.TrimSpace(request.promptCacheKey); value != "" {
		return "prompt_cache_key:" + isolateOpenAISessionID(apiKeyID, value), "prompt_cache_key"
	}
	if qoderIsClaudeCodeClient(c) {
		return qoderFallbackConversationKey(c, account, protocol, request, apiKeyID), "stable_seed"
	}
	if parsed := ParseMetadataUserID(request.metadataUserID); parsed != nil && strings.TrimSpace(parsed.SessionID) != "" {
		return "metadata_user_id:" + isolateOpenAISessionID(apiKeyID, parsed.SessionID), "metadata_user_id"
	}
	if value := qoderHeaderSessionID(c); value != "" {
		return "header:" + isolateOpenAISessionID(apiKeyID, value), "header"
	}
	return qoderFallbackConversationKey(c, account, protocol, request, apiKeyID), "fallback"
}

func qoderIsClaudeCodeClient(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if IsClaudeCodeClient(c.Request.Context()) {
		return true
	}
	return NewClaudeCodeValidator().ValidateUserAgent(c.Request.UserAgent())
}

func qoderHeaderSessionID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	for _, header := range []string{"session_id", "conversation_id", "x-session-id", "x-conversation-id", "X-Claude-Code-Session-Id"} {
		if value := strings.TrimSpace(c.Request.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

func qoderFallbackConversationKey(c *gin.Context, account *Account, protocol string, request qoderPayloadRequest, apiKeyID int64) string {
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	var clientDiscriminator string
	if c != nil && c.Request != nil {
		clientDiscriminator = sessionContextDiscriminator(&SessionContext{
			ClientIP:  ip.GetClientIP(c),
			UserAgent: c.GetHeader("User-Agent"),
			APIKeyID:  apiKeyID,
		})
	}
	firstUserText := ""
	for _, message := range request.messages {
		if message.Role == "user" && strings.TrimSpace(message.Text) != "" {
			firstUserText = message.Text
			break
		}
	}
	seed := strings.Join([]string{
		protocol,
		strings.TrimSpace(request.model),
		buildStableSessionSeed(accountID, clientDiscriminator, firstUserText),
	}, "::")
	return "stable_seed:" + qoderFingerprintString(seed)
}

func qoderMetadataUserID(raw any) string {
	metadata, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	return qoderStringField(metadata, "user_id")
}

func qoderConversationPrefixLen(previous, current []string) (int, bool) {
	if len(previous) > len(current) {
		return 0, false
	}
	for i := range previous {
		if previous[i] != current[i] {
			return 0, false
		}
	}
	return len(previous), true
}

func qoderMessageFingerprints(messages []qoderMessage) []string {
	fingerprints := make([]string, 0, len(messages))
	for _, message := range messages {
		fingerprints = append(fingerprints, qoderFingerprintAny(qoderPayloadMessageFromMessage(message)))
	}
	return fingerprints
}

func firstQoderFingerprint(fingerprints []string) string {
	if len(fingerprints) == 0 {
		return ""
	}
	return fingerprints[0]
}

func qoderFingerprintString(value string) string {
	return HashUsageRequestPayload([]byte(value))
}

func qoderSystemFingerprint(system string) string {
	return qoderFingerprintString(qoderNormalizeSystemForFingerprint(system))
}

func qoderNormalizeSystemForFingerprint(system string) string {
	if !strings.Contains(system, "x-anthropic-billing-header") || !strings.Contains(system, "cch=") {
		return system
	}
	return qoderClaudeBillingCCHRe.ReplaceAllString(system, "${1}<cch>${2}")
}

func qoderFingerprintAny(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return HashUsageRequestPayload([]byte(fmt.Sprint(value)))
	}
	return HashUsageRequestPayload(body)
}

func qoderJSONSize(value any) int {
	body, err := json.Marshal(value)
	if err != nil {
		return len([]byte(fmt.Sprint(value)))
	}
	return len(body)
}

func qoderSessionIDForConversation(key, systemFingerprint, toolsFingerprint string, messageFingerprints []string) string {
	var b strings.Builder
	_, _ = b.WriteString(key)
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(systemFingerprint)
	_, _ = b.WriteString("::")
	_, _ = b.WriteString(toolsFingerprint)
	_, _ = b.WriteString("::")
	for _, fingerprint := range messageFingerprints {
		_, _ = b.WriteString(fingerprint)
		_, _ = b.WriteString(",")
	}
	return generateSessionUUID(b.String())
}

func buildQoderPayloadWithOptions(request qoderPayloadRequest, sessionID string, messages []qoderMessage, includeSystem bool, includeTools bool) (map[string]any, string) {
	modelInfo := resolveQoderModel(request.model)
	userType := request.userType
	if strings.TrimSpace(userType) == "" {
		userType = "personal_standard"
	}

	requestID := uuid.NewString()
	prompt := latestQoderUserText(messages)
	if prompt == "" {
		prompt = latestQoderUserText(request.messages)
	}
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	payload := qoderBasePayload()
	payload["request_id"] = requestID
	payload["chat_record_id"] = requestID
	payload["request_set_id"] = uuid.NewString()
	payload["session_id"] = sessionID
	payload["aliyun_user_type"] = userType
	payload["parameters"].(map[string]any)["max_tokens"] = request.maxTokens
	payload["model_config"].(map[string]any)["key"] = modelInfo.Key
	payload["model_config"].(map[string]any)["source"] = modelInfo.Source
	payload["chat_context"].(map[string]any)["text"].(map[string]any)["text"] = prompt
	extra := payload["chat_context"].(map[string]any)["extra"].(map[string]any)
	extra["modelConfig"].(map[string]any)["key"] = modelInfo.Key
	extra["modelConfig"].(map[string]any)["source"] = modelInfo.Source
	extra["originalContent"].(map[string]any)["text"] = prompt
	payload["business"] = map[string]any{
		"product":  "cli",
		"version":  "1.0.20",
		"type":     "agent",
		"stage":    "init",
		"id":       uuid.NewString(),
		"name":     truncateRunes(prompt, 30),
		"begin_at": time.Now().UnixMilli(),
	}

	outMessages := make([]any, 0, len(messages)+1)
	if includeSystem && strings.TrimSpace(request.system) != "" {
		outMessages = append(outMessages, qoderPayloadMessage("system", request.system))
	}
	for _, msg := range messages {
		outMessages = append(outMessages, qoderPayloadMessageFromMessage(msg))
	}
	payload["messages"] = outMessages
	if includeTools {
		payload["tools"] = request.tools
	} else {
		payload["tools"] = []any{}
	}
	return payload, modelInfo.Key
}

func qoderBasePayload() map[string]any {
	return map[string]any{
		"request_id":       "",
		"request_set_id":   "",
		"chat_record_id":   "",
		"stream":           true,
		"chat_task":        "FREE_INPUT",
		"image_urls":       nil,
		"is_reply":         true,
		"is_retry":         false,
		"session_id":       "",
		"code_language":    "",
		"source":           1,
		"version":          "3",
		"chat_prompt":      "",
		"parameters":       map[string]any{"max_tokens": qoderDefaultMaxTokens},
		"aliyun_user_type": "personal_standard",
		"session_type":     "qodercli",
		"agent_id":         "agent_common",
		"task_id":          "common",
		"chat_context": map[string]any{
			"chatPrompt": "",
			"features":   []any{},
			"imageUrls":  nil,
			"text":       map[string]any{"type": "text", "text": ""},
			"extra": map[string]any{
				"context":         []any{},
				"modelConfig":     map[string]any{"is_reasoning": false, "key": "auto"},
				"originalContent": map[string]any{"type": "text", "text": ""},
			},
		},
		"model_config": map[string]any{
			"key":              "auto",
			"display_name":     "Qoder Auto",
			"model":            "",
			"format":           "openai",
			"is_vl":            false,
			"is_reasoning":     false,
			"api_key":          "",
			"url":              "",
			"source":           "system",
			"max_input_tokens": 180000,
		},
		"messages": []any{},
		"tools":    []any{},
	}
}

var qoderBlankResponseMeta = map[string]any{
	"id": "",
	"usage": map[string]any{
		"prompt_tokens":     0,
		"completion_tokens": 0,
		"total_tokens":      0,
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": 0,
		},
		"prompt_tokens_details": map[string]any{
			"cached_tokens": 0,
		},
	},
}

func qoderPayloadMessage(role, text string) map[string]any {
	return qoderPayloadMessageFromMessage(qoderMessage{Role: role, Text: text})
}

func qoderPayloadMessageFromMessage(message qoderMessage) map[string]any {
	isUser := message.Role == "user"
	content := message.Text
	if isUser {
		content = ""
	}
	msg := map[string]any{
		"role":                        message.Role,
		"content":                     content,
		"contents":                    []any{},
		"reasoning_content_signature": "",
		"response_meta":               qoderBlankResponseMeta,
	}
	if message.Text != "" {
		msg["contents"] = []any{map[string]any{"type": "text", "text": message.Text}}
	}
	copyQoderToolFields(msg, message.Raw)
	if strings.TrimSpace(message.ToolCallID) != "" {
		msg["tool_call_id"] = strings.TrimSpace(message.ToolCallID)
	}
	return msg
}

func copyQoderToolFields(out map[string]any, raw map[string]any) {
	if len(raw) == 0 {
		return
	}
	if toolCalls := normalizeQoderToolCalls(raw["tool_calls"]); len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	} else if toolCalls := anthropicToolUseBlocksToQoderToolCalls(raw["content"]); len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	}
	if toolCallID, ok := raw["tool_call_id"].(string); ok && strings.TrimSpace(toolCallID) != "" {
		out["tool_call_id"] = toolCallID
	}
	if name, ok := raw["name"].(string); ok && strings.TrimSpace(name) != "" {
		out["name"] = name
	}
}

func normalizeQoderToolCalls(raw any) []any {
	tools := qoderAnySlice(raw)
	if len(tools) == 0 {
		return []any{}
	}
	normalized := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, _ := tool["function"].(map[string]any)
		name := qoderStringField(function, "name")
		arguments := qoderToolArgumentsString(function["arguments"])
		if name == "" && strings.TrimSpace(arguments) == "" {
			continue
		}
		id := qoderStringField(tool, "id")
		callType := qoderStringField(tool, "type")
		if callType == "" {
			callType = "function"
		}
		normalized = append(normalized, map[string]any{
			"id":   id,
			"type": callType,
			"function": map[string]any{
				"name":      name,
				"arguments": arguments,
			},
		})
	}
	return normalized
}

func anthropicToolUseBlocksToQoderToolCalls(raw any) []any {
	blocks := qoderAnySlice(raw)
	if len(blocks) == 0 {
		return []any{}
	}
	toolCalls := make([]any, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		name := qoderStringField(block, "name")
		if name == "" {
			continue
		}
		toolCalls = append(toolCalls, map[string]any{
			"id":   qoderStringField(block, "id"),
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": qoderToolArgumentsString(block["input"]),
			},
		})
	}
	return toolCalls
}

func qoderToolArgumentsString(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.RawMessage:
		return string(v)
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(body)
	}
}

func convertAnthropicToolsToQoderTools(raw any) []any {
	tools := qoderAnySlice(raw)
	if len(tools) == 0 {
		return []any{}
	}
	converted := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if toolType, _ := tool["type"].(string); toolType == "function" {
			if _, ok := tool["function"].(map[string]any); ok {
				converted = append(converted, tool)
				continue
			}
		}
		name := strings.TrimSpace(fmt.Sprint(tool["name"]))
		if name == "" {
			continue
		}
		function := map[string]any{"name": name}
		if description, ok := tool["description"].(string); ok && strings.TrimSpace(description) != "" {
			function["description"] = description
		}
		if inputSchema, ok := tool["input_schema"]; ok {
			function["parameters"] = inputSchema
		} else if parameters, ok := tool["parameters"]; ok {
			function["parameters"] = parameters
		} else {
			function["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if strict, ok := tool["strict"]; ok {
			function["strict"] = strict
		}
		converted = append(converted, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return converted
}

func ReadQoderSSEEvents(resp *http.Response) ([]qoder.SSEEvent, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("qoder response body is nil")
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
	events := make([]qoder.SSEEvent, 0)
	for scanner.Scan() {
		parsed, err := qoder.ParseSSELine(scanner.Text())
		if err != nil {
			return nil, err
		}
		for _, event := range parsed {
			events = append(events, event)
			if event.IsDone {
				return events, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func WriteQoderOpenAIStream(c *gin.Context, model string, events []qoder.SSEEvent) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil)); err != nil {
		return err
	}
	usage := ClaudeUsage{}
	totalTokens := 0
	toolCalls := newQoderOpenAIToolCallAccumulator()
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
			totalTokens = event.TotalTokens
			if err := writeSSEData(c.Writer, openAIUsageChunk(completionID, model, usage, totalTokens)); err != nil {
				return err
			}
			continue
		}
		if event.IsDone {
			finishReason := "stop"
			if toolCalls.HasToolCalls() {
				finishReason = "tool_calls"
			}
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{}, finishReason)); err != nil {
				return err
			}
			_, err := io.WriteString(c.Writer, "data: [DONE]\n\n")
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return err
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"content": event.Text}, nil)); err != nil {
				return err
			}
		}
		if event.Type == "tool_call_delta" {
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"tool_calls": toolCalls.AppendDelta(event)}, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

type qoderStreamResult struct {
	Usage       ClaudeUsage
	TotalTokens int
}

type qoderOpenAIToolCallAccumulator struct {
	calls               []qoderOpenAIToolCallState
	slotByUpstreamIndex map[int]int
}

type qoderOpenAIToolCallState struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

func newQoderOpenAIToolCallAccumulator() *qoderOpenAIToolCallAccumulator {
	return &qoderOpenAIToolCallAccumulator{}
}

func (a *qoderOpenAIToolCallAccumulator) AppendDelta(event qoder.SSEEvent) []any {
	if a == nil {
		return []any{}
	}
	index := a.resolveIndex(event)
	for len(a.calls) <= index {
		a.calls = append(a.calls, qoderOpenAIToolCallState{Type: "function"})
	}
	state := &a.calls[index]
	if event.ToolCallID != "" {
		state.ID = event.ToolCallID
	}
	if event.ToolType != "" {
		state.Type = event.ToolType
	} else if state.Type == "" {
		state.Type = "function"
	}
	if event.ToolName != "" {
		state.Name = event.ToolName
	}
	if event.Arguments != "" {
		state.Arguments += event.Arguments
	}
	a.bindUpstreamIndex(event, index)
	return []any{qoderOpenAIToolCallDelta(index, event)}
}

func (a *qoderOpenAIToolCallAccumulator) Calls() []any {
	if a == nil || len(a.calls) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(a.calls))
	for _, call := range a.calls {
		if call.ID == "" && call.Name == "" && call.Arguments == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":   call.ID,
			"type": firstNonEmptyQoder(call.Type, "function"),
			"function": map[string]any{
				"name":      call.Name,
				"arguments": call.Arguments,
			},
		})
	}
	return out
}

func (a *qoderOpenAIToolCallAccumulator) HasToolCalls() bool {
	return len(a.Calls()) > 0
}

func (a *qoderOpenAIToolCallAccumulator) resolveIndex(event qoder.SSEEvent) int {
	if event.ToolCallID != "" {
		for i := range a.calls {
			if a.calls[i].ID == event.ToolCallID {
				return i
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 {
		if index, ok := a.slotByUpstreamIndex[event.ToolCallIndex]; ok && index >= 0 && index < len(a.calls) {
			if event.ToolCallID == "" || a.calls[index].ID == "" || a.calls[index].ID == event.ToolCallID {
				return index
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 && event.ToolCallID == "" && (event.ToolName != "" || event.ToolType != "") {
		return event.ToolCallIndex
	}
	if len(a.calls) > 0 {
		last := &a.calls[len(a.calls)-1]
		if event.ToolCallID == "" || last.ID == "" || last.ID == event.ToolCallID {
			return len(a.calls) - 1
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 {
		return event.ToolCallIndex
	}
	if len(a.calls) == 0 {
		return 0
	}
	return len(a.calls)
}

func (a *qoderOpenAIToolCallAccumulator) bindUpstreamIndex(event qoder.SSEEvent, slot int) {
	if a == nil || !event.HasToolCallIndex || event.ToolCallIndex < 0 || slot < 0 || slot >= len(a.calls) {
		return
	}
	if a.slotByUpstreamIndex == nil {
		a.slotByUpstreamIndex = make(map[int]int)
	}
	if existingSlot, ok := a.slotByUpstreamIndex[event.ToolCallIndex]; ok && existingSlot >= 0 && existingSlot < len(a.calls) && existingSlot != slot {
		existingID := a.calls[existingSlot].ID
		slotID := a.calls[slot].ID
		if existingID != "" && slotID != "" && existingID != slotID {
			return
		}
	}
	a.slotByUpstreamIndex[event.ToolCallIndex] = slot
}

func qoderOpenAIToolCallDelta(index int, event qoder.SSEEvent) map[string]any {
	function := map[string]any{}
	if event.ToolName != "" {
		function["name"] = event.ToolName
	}
	if event.Arguments != "" {
		function["arguments"] = event.Arguments
	}
	callType := event.ToolType
	if callType == "" && (event.ToolCallID != "" || event.ToolName != "") {
		callType = "function"
	}
	delta := map[string]any{
		"index":    index,
		"function": function,
	}
	if event.ToolCallID != "" {
		delta["id"] = event.ToolCallID
	}
	if callType != "" {
		delta["type"] = callType
	}
	return delta
}

func WriteQoderOpenAIStreamResponse(c *gin.Context, model string, resp *http.Response) (*qoderStreamResult, error) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	completionID := "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"role": "assistant"}, nil)); err != nil {
		closeQoderResponse(resp)
		return nil, err
	}
	result := &qoderStreamResult{}
	toolCalls := newQoderOpenAIToolCallAccumulator()
	if err := streamQoderEvents(resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			result.TotalTokens = event.TotalTokens
			return writeSSEData(c.Writer, openAIUsageChunk(completionID, model, result.Usage, result.TotalTokens))
		}
		if event.IsDone {
			finishReason := "stop"
			if toolCalls.HasToolCalls() {
				finishReason = "tool_calls"
			}
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{}, finishReason)); err != nil {
				return err
			}
			_, err := io.WriteString(c.Writer, "data: [DONE]\n\n")
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			return err
		}
		if event.Type == "text_delta" && event.Text != "" {
			return writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"content": event.Text}, nil))
		}
		if event.Type == "tool_call_delta" {
			return writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"tool_calls": toolCalls.AppendDelta(event)}, nil))
		}
		return nil
	}, func() error {
		_, err := io.WriteString(c.Writer, ": keep-alive\n\n")
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func WriteQoderAnthropicStream(c *gin.Context, model string, events []qoder.SSEEvent) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := writeAnthropicSSE(c.Writer, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		return err
	}
	usage := ClaudeUsage{}
	writer := newQoderAnthropicContentWriter(c.Writer)
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
			continue
		}
		if event.IsDone {
			if err := writer.closeOpenBlock(); err != nil {
				return err
			}
			if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   writer.stopReason(),
					"stop_sequence": nil,
				},
				"usage": qoderAnthropicUsage(usage),
			}); err != nil {
				return err
			}
			return writeAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := writer.writeTextDelta(event.Text); err != nil {
				return err
			}
			continue
		}
		if event.Type == "reasoning_delta" {
			continue
		}
		if event.Type == "tool_call_delta" {
			if err := writer.writeToolCall(event); err != nil {
				return err
			}
		}
	}
	return nil
}

func WriteQoderAnthropicStreamResponse(c *gin.Context, model string, resp *http.Response) (*qoderStreamResult, error) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	messageID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := writeAnthropicSSE(c.Writer, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		closeQoderResponse(resp)
		return nil, err
	}
	result := &qoderStreamResult{}
	writer := newQoderAnthropicContentWriter(c.Writer)
	if err := streamQoderEvents(resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			return nil
		}
		if event.IsDone {
			if err := writer.closeOpenBlock(); err != nil {
				return err
			}
			if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   writer.stopReason(),
					"stop_sequence": nil,
				},
				"usage": qoderAnthropicUsage(result.Usage),
			}); err != nil {
				return err
			}
			return writeAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
		}
		if event.Type == "text_delta" && event.Text != "" {
			return writer.writeTextDelta(event.Text)
		}
		if event.Type == "reasoning_delta" {
			return nil
		}
		if event.Type == "tool_call_delta" {
			return writer.writeToolCall(event)
		}
		return nil
	}, func() error {
		_, err := io.WriteString(c.Writer, ": keep-alive\n\n")
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}); err != nil {
		return nil, err
	}
	return result, nil
}

type qoderAnthropicContentWriter struct {
	w                io.Writer
	nextIndex        int
	openTextIndex    *int
	pendingToolCalls *qoderOpenAIToolCallAccumulator
	sawToolCall      bool
}

func newQoderAnthropicContentWriter(w io.Writer) *qoderAnthropicContentWriter {
	return &qoderAnthropicContentWriter{w: w}
}

func (w *qoderAnthropicContentWriter) writeTextDelta(text string) error {
	if strings.TrimSpace(text) == "" && text == "" {
		return nil
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if w.openTextIndex == nil {
		index := w.nextIndex
		w.nextIndex++
		if err := writeAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
		w.openTextIndex = &index
	}
	return writeAnthropicSSE(w.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": *w.openTextIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (w *qoderAnthropicContentWriter) writeToolCall(event qoder.SSEEvent) error {
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	w.sawToolCall = true
	if w.pendingToolCalls == nil {
		w.pendingToolCalls = newQoderOpenAIToolCallAccumulator()
	}
	w.pendingToolCalls.AppendDelta(event)
	return nil
}

func (w *qoderAnthropicContentWriter) closeOpenBlock() error {
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	return w.flushPendingToolCalls()
}

func (w *qoderAnthropicContentWriter) closeOpenTextBlock() error {
	if w.openTextIndex == nil {
		return nil
	}
	index := *w.openTextIndex
	w.openTextIndex = nil
	return writeAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (w *qoderAnthropicContentWriter) flushPendingToolCalls() error {
	if w.pendingToolCalls == nil {
		return nil
	}
	calls := w.pendingToolCalls.Calls()
	w.pendingToolCalls = nil
	for _, rawCall := range calls {
		call, ok := rawCall.(map[string]any)
		if !ok {
			continue
		}
		function, _ := call["function"].(map[string]any)
		index := w.nextIndex
		w.nextIndex++
		if err := writeAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    qoderStringField(call, "id"),
				"name":  qoderStringField(function, "name"),
				"input": map[string]any{},
			},
		}); err != nil {
			return err
		}
		arguments := qoderToolArgumentsString(function["arguments"])
		if arguments != "" {
			if err := writeAnthropicSSE(w.w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": index,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": arguments,
				},
			}); err != nil {
				return err
			}
		}
		if err := writeAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index}); err != nil {
			return err
		}
	}
	return nil
}

func (w *qoderAnthropicContentWriter) stopReason() string {
	if w != nil && w.sawToolCall {
		return "tool_use"
	}
	return "end_turn"
}

func streamQoderEvents(resp *http.Response, handle func(qoder.SSEEvent) error, keepalive func() error) error {
	if resp == nil || resp.Body == nil {
		return errors.New("qoder response body is nil")
	}
	defer resp.Body.Close()

	type eventResult struct {
		events []qoder.SSEEvent
		err    error
	}
	results := make(chan eventResult, 1)
	go func() {
		defer close(results)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
		for scanner.Scan() {
			parsed, err := qoder.ParseSSELine(scanner.Text())
			if err != nil {
				results <- eventResult{err: err}
				return
			}
			if len(parsed) > 0 {
				results <- eventResult{events: parsed}
			}
			for _, event := range parsed {
				if event.IsDone {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			results <- eventResult{err: err}
		}
	}()

	ticker := time.NewTicker(qoderKeepaliveEvery)
	defer ticker.Stop()
	for {
		select {
		case result, ok := <-results:
			if !ok {
				return nil
			}
			if result.err != nil {
				return result.err
			}
			for _, event := range result.events {
				if err := handle(event); err != nil {
					return err
				}
				if event.IsDone {
					return nil
				}
			}
		case <-ticker.C:
			if keepalive != nil {
				if err := keepalive(); err != nil {
					return err
				}
			}
		}
	}
}

func closeQoderResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func BuildQoderOpenAICompletion(model string, events []qoder.SSEEvent) ([]byte, error) {
	content := qoderTextFromEvents(events)
	usage := qoderUsageFromEvents(events)
	totalTokens := qoderTotalTokensFromEvents(events, usage)
	toolCalls := qoderOpenAIToolCallsFromEvents(events)
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
		finishReason = "tool_calls"
	}
	return json.Marshal(map[string]any{
		"id":      "chatcmpl-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24],
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": qoderOpenAIUsage(usage, totalTokens),
	})
}

func BuildQoderAnthropicMessage(model string, events []qoder.SSEEvent) ([]byte, error) {
	contentBlocks := qoderAnthropicContentBlocksFromEvents(events)
	usage := qoderUsageFromEvents(events)
	stopReason := "end_turn"
	if qoderEventsHaveToolCalls(events) {
		stopReason = "tool_use"
	}
	return json.Marshal(map[string]any{
		"id":            "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         qoderAnthropicUsage(usage),
	})
}

func qoderOpenAIToolCallsFromEvents(events []qoder.SSEEvent) []any {
	acc := newQoderOpenAIToolCallAccumulator()
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.Calls()
}

func qoderEventsHaveToolCalls(events []qoder.SSEEvent) bool {
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			return true
		}
	}
	return false
}

func qoderAnthropicContentBlocksFromEvents(events []qoder.SSEEvent) []any {
	blocks := make([]any, 0)
	var text bytes.Buffer
	var toolAccumulator *qoderOpenAIToolCallAccumulator
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": text.String()})
		text.Reset()
	}
	flushTools := func() {
		if toolAccumulator == nil {
			return
		}
		for _, rawCall := range toolAccumulator.Calls() {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			function, _ := call["function"].(map[string]any)
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    call["id"],
				"name":  function["name"],
				"input": qoderAnthropicToolInput(function["arguments"]),
			})
		}
		toolAccumulator = nil
	}
	for _, event := range events {
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				flushTools()
				text.WriteString(event.Text)
			}
		case "reasoning_delta":
			continue
		case "tool_call_delta":
			flushText()
			if toolAccumulator == nil {
				toolAccumulator = newQoderOpenAIToolCallAccumulator()
			}
			toolAccumulator.AppendDelta(event)
		}
	}
	flushText()
	flushTools()
	if len(blocks) == 0 {
		return []any{map[string]any{"type": "text", "text": ""}}
	}
	return blocks
}

func qoderAnthropicToolInput(raw any) map[string]any {
	args := qoderToolArgumentsString(raw)
	if strings.TrimSpace(args) == "" {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil {
		return decoded
	}
	return map[string]any{"raw": args}
}

func qoderUsageFromEvents(events []qoder.SSEEvent) ClaudeUsage {
	usage := ClaudeUsage{}
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
		}
	}
	return usage
}

func mergeQoderUsageEvent(usage *ClaudeUsage, event qoder.SSEEvent) {
	if usage == nil || !event.HasUsage {
		return
	}
	usage.InputTokens = event.PromptTokens
	usage.OutputTokens = event.CompletionTokens
}

func qoderTotalTokensFromEvents(events []qoder.SSEEvent, usage ClaudeUsage) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].HasUsage && events[i].TotalTokens > 0 {
			return events[i].TotalTokens
		}
	}
	return usage.InputTokens + usage.OutputTokens
}

func qoderOpenAIUsage(usage ClaudeUsage, totalTokens int) map[string]any {
	promptTokens := usage.InputTokens
	completionTokens := usage.OutputTokens
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	return map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
}

func qoderAnthropicUsage(usage ClaudeUsage) map[string]any {
	return map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	}
}

func writeSSEData(w io.Writer, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func writeAnthropicSSE(w io.Writer, event string, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func openAIChunk(id, model string, delta map[string]any, finishReason any) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
}

func openAIUsageChunk(id, model string, usage ClaudeUsage, totalTokens int) map[string]any {
	chunk := openAIChunk(id, model, map[string]any{}, nil)
	chunk["usage"] = qoderOpenAIUsage(usage, totalTokens)
	return chunk
}

func resolveQoderModel(model string) qoderModelInfo {
	if info, ok := lookupQoderModelAlias(strings.TrimSpace(model)); ok {
		return info
	}
	return qoderModelInfo{Key: strings.TrimSpace(model), Source: "system"}
}

func qoderUserType(account *Account) string {
	if account == nil {
		return "personal_standard"
	}
	return firstNonEmptyQoder(account.GetCredential("user_type"), "personal_standard")
}

func latestQoderUserText(messages []qoderMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Text) != "" {
			return messages[i].Text
		}
	}
	return ""
}

func openAIMessageText(msg map[string]any) string {
	if content, ok := msg["content"].(string); ok {
		return content
	}
	if parts, ok := msg["content"].([]any); ok {
		textParts := make([]string, 0, len(parts))
		for _, part := range parts {
			partMap, ok := part.(map[string]any)
			if !ok || partMap["type"] != "text" {
				continue
			}
			if text, ok := partMap["text"].(string); ok {
				textParts = append(textParts, text)
			}
		}
		return strings.Join(textParts, "\n")
	}
	return ""
}

func anthropicSystemText(raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] != "text" {
				return "", fmt.Errorf("unsupported system block type: %v", block["type"])
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", errors.New("unsupported system field")
	}
}

func anthropicMessageToQoderMessages(msg map[string]any) ([]qoderMessage, error) {
	role, _ := msg["role"].(string)
	if role != "user" {
		text, err := anthropicContentText(msg["content"])
		if err != nil {
			return nil, err
		}
		return []qoderMessage{{Role: role, Text: text, Raw: msg}}, nil
	}
	blocks := qoderAnySlice(msg["content"])
	if len(blocks) == 0 {
		text, err := anthropicContentText(msg["content"])
		if err != nil {
			return nil, err
		}
		return []qoderMessage{{Role: role, Text: text, Raw: msg}}, nil
	}

	var messages []qoderMessage
	var textParts []string
	flushText := func() {
		text := strings.Join(nonEmptyStrings(textParts), "\n")
		textParts = nil
		if text == "" {
			return
		}
		messages = append(messages, qoderMessage{
			Role: "user",
			Text: text,
			Raw:  map[string]any{"role": "user", "content": text},
		})
	}
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		switch block["type"] {
		case "text":
			if text, ok := block["text"].(string); ok {
				textParts = append(textParts, text)
			}
		case "tool_result":
			flushText()
			toolCallID := qoderStringField(block, "tool_use_id")
			toolText := anthropicToolResultText(block["content"])
			raw := map[string]any{
				"role":         "tool",
				"tool_call_id": toolCallID,
				"content":      toolText,
			}
			messages = append(messages, qoderMessage{Role: "tool", Text: toolText, ToolCallID: toolCallID, Raw: raw})
		case "tool_use":
			// Qoder payload.py ignores tool_use when flattening message text.
		case "thinking", "redacted_thinking":
			// These blocks can be returned by Claude clients in assistant history.
			// Qoder does not accept Anthropic thinking signatures, so do not leak them
			// into the upstream prompt or fail the tool-result follow-up turn.
		default:
			return nil, fmt.Errorf("unsupported content block type: %v", block["type"])
		}
	}
	flushText()
	if len(messages) == 0 {
		return []qoderMessage{{Role: role, Raw: msg}}, nil
	}
	return messages, nil
}

func anthropicContentText(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			case "tool_result":
				parts = append(parts, anthropicToolResultText(block["content"]))
			case "tool_use":
				// Qoder payload.py ignores tool_use when flattening message text.
			case "thinking", "redacted_thinking":
				// Ignore Anthropic thinking history for Qoder compatibility.
			default:
				return "", fmt.Errorf("unsupported content block type: %v", block["type"])
			}
		}
		return strings.Join(nonEmptyStrings(parts), "\n"), nil
	default:
		return "", nil
	}
}

func anthropicToolResultText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func qoderAnySlice(raw any) []any {
	if raw == nil {
		return []any{}
	}
	if values, ok := raw.([]any); ok {
		return values
	}
	return []any{}
}

func qoderStringField(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func numberAsInt(raw any, fallback int) int {
	switch v := raw.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return int(n)
		}
	}
	return fallback
}

func qoderTextFromEvents(events []qoder.SSEEvent) string {
	var buf bytes.Buffer
	for _, event := range events {
		if event.Type == "text_delta" && event.Text != "" {
			buf.WriteString(event.Text)
		}
	}
	return buf.String()
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func gjsonString(body []byte, path string) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	value, _ := decoded[path].(string)
	return value
}

func gjsonBool(body []byte, path string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	value, _ := decoded[path].(bool)
	return value
}
