package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
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

	qoderTextToolCallStart = "<tool_call>"
	qoderTextToolCallEnd   = "</tool_call>"
	qoderTextArgKeyStart   = "<arg_key>"
	qoderTextArgKeyEnd     = "</arg_key>"
	qoderTextArgValueStart = "<arg_value>"
	qoderTextArgValueEnd   = "</arg_value>"

	qoderDSMLToolCallsStart = "<｜｜DSML｜｜tool_calls>"
	qoderDSMLToolCallsEnd   = "</｜｜DSML｜｜tool_calls>"
	qoderDSMLInvokeStart    = "<｜｜DSML｜｜invoke"
	qoderDSMLInvokeEnd      = "</｜｜DSML｜｜invoke>"
	qoderDSMLParameterStart = "<｜｜DSML｜｜parameter"
	qoderDSMLParameterEnd   = "</｜｜DSML｜｜parameter>"
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
	conversationPlan.commitAccepted()

	var responseBody []byte
	var upstreamUsage ClaudeUsage
	var recordUsage ClaudeUsage
	commitCompleteConversation := true
	toolNameMapper := qoderDeclaredToolNameMapper(qoderAnySlice(payload["tools"]))
	if clientStream {
		streamResult, err := WriteQoderOpenAIStreamResponse(
			streamCtx,
			c,
			requestModel,
			resp,
			qoderOpenAIStreamUsageMapper(conversationPlan.recordUsage),
			qoderOpenAIStreamToolNameMapper(toolNameMapper),
		)
		if err != nil {
			conversationPlan.rollbackAccepted()
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = streamResult.Usage
		recordUsage = conversationPlan.recordUsage(upstreamUsage)
		commitCompleteConversation = streamResult.HasOutput
	} else {
		events, err := ReadQoderSSEEventsContext(streamCtx, resp, qoderNonStreamingKeepalive(c))
		if err != nil {
			conversationPlan.rollbackAccepted()
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = qoderUsageFromEvents(events)
		recordUsage = conversationPlan.recordUsage(upstreamUsage)
		responseBody, err = BuildQoderOpenAICompletion(requestModel, qoderEventsWithUsage(events, upstreamUsage), toolNameMapper)
		if err != nil {
			conversationPlan.rollbackAccepted()
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}
	conversationPlan.logUsage(c, account, upstreamUsage, recordUsage)
	if commitCompleteConversation {
		conversationPlan.commit(upstreamUsage)
	}

	return &ForwardResult{
		Model:         requestModel,
		UpstreamModel: modelKey,
		Usage:         recordUsage,
		Stream:        clientStream,
		Duration:      time.Since(start),
		ResponseBody:  responseBody,
	}, nil
}

func (s *QoderGatewayService) ForwardResponses(ctx context.Context, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
	start := time.Now()
	streamCtx, cancel := context.WithTimeout(ctx, qoderStreamTimeout)
	defer cancel()

	requestModel := strings.TrimSpace(gjsonString(body, "model"))
	body = applyQoderAccountModelMapping(account, body)
	request, err := parseQoderResponsesPayload(body)
	if err != nil {
		return nil, err
	}
	clientStream := gjsonBool(body, "stream")
	result := s.buildQoderPayloadWithConversation(c, account, "openai_responses", request)
	payload := result.Payload
	modelKey := result.ModelKey
	conversationPlan := result.Plan
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal qoder payload: %w", err)
	}

	resp, err := s.openQoderStream(streamCtx, account, payloadBody, modelKey)
	if err != nil {
		s.applyUpstreamErrorPolicy(ctx, account, err)
		return nil, err
	}
	conversationPlan.commitAccepted()

	var responseBody []byte
	var upstreamUsage ClaudeUsage
	var recordUsage ClaudeUsage
	commitCompleteConversation := true
	toolNameMapper := qoderDeclaredToolNameMapper(qoderAnySlice(payload["tools"]))
	if clientStream {
		streamResult, err := WriteQoderResponsesStreamResponse(
			streamCtx,
			c,
			requestModel,
			resp,
			qoderResponsesStreamUsageMapper(conversationPlan.recordUsage),
			qoderResponsesStreamToolNameMapper(toolNameMapper),
		)
		if err != nil {
			conversationPlan.rollbackAccepted()
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = streamResult.Usage
		recordUsage = conversationPlan.recordUsage(upstreamUsage)
		commitCompleteConversation = streamResult.HasOutput
	} else {
		events, err := ReadQoderSSEEventsContext(streamCtx, resp, qoderNonStreamingKeepalive(c))
		if err != nil {
			conversationPlan.rollbackAccepted()
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = qoderUsageFromEvents(events)
		recordUsage = conversationPlan.recordUsage(upstreamUsage)
		responseBody, err = BuildQoderResponsesResponse(requestModel, qoderEventsWithUsage(events, recordUsage), toolNameMapper)
		if err != nil {
			conversationPlan.rollbackAccepted()
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}
	conversationPlan.logUsage(c, account, upstreamUsage, recordUsage)
	if commitCompleteConversation {
		conversationPlan.commit(upstreamUsage)
	}

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
	conversationPlan.commitAccepted()

	var responseBody []byte
	var upstreamUsage ClaudeUsage
	var recordUsage ClaudeUsage
	commitCompleteConversation := true
	toolNameMapper := qoderDeclaredToolNameMapper(qoderAnySlice(payload["tools"]))
	if clientStream {
		streamResult, err := WriteQoderAnthropicStreamResponse(streamCtx, c, requestModel, resp, qoderAnthropicStreamUsageMapper(conversationPlan.recordUsage), qoderAnthropicStreamToolNameMapper(toolNameMapper))
		if err != nil {
			conversationPlan.rollbackAccepted()
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = streamResult.Usage
		recordUsage = conversationPlan.recordUsage(upstreamUsage)
		commitCompleteConversation = streamResult.HasOutput
	} else {
		events, err := ReadQoderSSEEventsContext(streamCtx, resp, qoderNonStreamingKeepalive(c))
		if err != nil {
			conversationPlan.rollbackAccepted()
			s.applyUpstreamErrorPolicy(ctx, account, err)
			return nil, err
		}
		upstreamUsage = qoderUsageFromEvents(events)
		recordUsage = conversationPlan.recordUsage(upstreamUsage)
		responseBody, err = BuildQoderAnthropicMessage(requestModel, qoderEventsWithUsage(events, upstreamUsage), toolNameMapper)
		if err != nil {
			conversationPlan.rollbackAccepted()
			return nil, err
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", responseBody)
	}
	conversationPlan.logUsage(c, account, upstreamUsage, recordUsage)
	if commitCompleteConversation {
		conversationPlan.commit(upstreamUsage)
	}

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
	request.userType = qoderUserType(account)
	store := s.qoderConversationStore()
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
	var req apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return qoderPayloadRequest{}, fmt.Errorf("parse chat completions request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return qoderPayloadRequest{}, errors.New("model is required")
	}

	messages, err := qoderMessagesFromChatCompletions(req.Messages)
	if err != nil {
		return qoderPayloadRequest{}, err
	}
	messages = enrichQoderToolResultMessages(messages)

	rawReq := qoderRequestMap(body)
	maxTokens := qoderDefaultMaxTokens
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		maxTokens = *req.MaxTokens
	}
	return qoderPayloadRequest{
		model:           req.Model,
		system:          qoderChatSystemText(req.Messages),
		messages:        messages,
		tools:           qoderAnySlice(rawReq["tools"]),
		maxTokens:       maxTokens,
		explicitSession: firstNonEmptyQoder(qoderStringField(rawReq, "session_id"), qoderStringField(rawReq, "conversation_id")),
		promptCacheKey:  qoderStringField(rawReq, "prompt_cache_key"),
		metadataUserID:  qoderMetadataUserID(rawReq["metadata"]),
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

func parseQoderResponsesPayload(body []byte) (qoderPayloadRequest, error) {
	var req apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return qoderPayloadRequest{}, fmt.Errorf("parse responses request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return qoderPayloadRequest{}, errors.New("model is required")
	}
	messages, err := qoderMessagesFromResponsesInput(req.Input)
	if err != nil {
		return qoderPayloadRequest{}, err
	}
	messages = enrichQoderToolResultMessages(messages)
	system := strings.TrimSpace(req.Instructions)
	rawReq := qoderRequestMap(body)
	maxTokens := qoderDefaultMaxTokens
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		maxTokens = *req.MaxOutputTokens
	}
	return qoderPayloadRequest{
		model:           req.Model,
		system:          system,
		messages:        messages,
		tools:           qoderResponsesToolsToQoderTools(req.Tools),
		maxTokens:       maxTokens,
		explicitSession: firstNonEmptyQoder(qoderStringField(rawReq, "session_id"), qoderStringField(rawReq, "conversation_id"), qoderStringField(rawReq, "previous_response_id")),
		promptCacheKey:  qoderStringField(rawReq, "prompt_cache_key"),
		metadataUserID:  qoderMetadataUserID(rawReq["metadata"]),
	}, nil
}

func parseQoderAnthropicMessagesPayload(body []byte) (qoderPayloadRequest, error) {
	var req apicompat.AnthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return qoderPayloadRequest{}, fmt.Errorf("parse anthropic messages request: %w", err)
	}
	if strings.TrimSpace(req.Model) == "" {
		return qoderPayloadRequest{}, errors.New("model is required")
	}

	system, err := qoderAnthropicSystemText(req.System)
	if err != nil {
		return qoderPayloadRequest{}, err
	}
	toolReq := req
	toolReq.System = nil
	toolReq.Messages = nil
	responsesReq, err := apicompat.AnthropicToResponses(&toolReq)
	if err != nil {
		return qoderPayloadRequest{}, fmt.Errorf("convert anthropic messages request: %w", err)
	}
	messages, err := qoderMessagesFromAnthropicMessages(req.Messages)
	if err != nil {
		return qoderPayloadRequest{}, err
	}
	messages = enrichQoderToolResultMessages(messages)

	rawReq := qoderRequestMap(body)
	maxTokens := qoderDefaultMaxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}
	return qoderPayloadRequest{
		model:           req.Model,
		system:          system,
		messages:        messages,
		tools:           qoderResponsesToolsToQoderTools(responsesReq.Tools),
		maxTokens:       maxTokens,
		explicitSession: firstNonEmptyQoder(qoderStringField(rawReq, "session_id"), qoderStringField(rawReq, "conversation_id")),
		promptCacheKey:  qoderStringField(rawReq, "prompt_cache_key"),
		metadataUserID:  qoderMetadataUserID(rawReq["metadata"]),
	}, nil
}

type qoderMessage struct {
	Role        string
	Text        string
	ToolCallID  string
	Raw         map[string]any
	TextContent []map[string]any
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
	previousState         *qoderConversationState
	acceptedCommitted     bool
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
			previousState:         cloneQoderConversationState(state),
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
		messagesToSend:        messages,
		includeSystem:         true,
		includeTools:          true,
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
		previousState:         cloneQoderConversationState(state),
	}
}

func qoderMessageHasToolCalls(message qoderMessage) bool {
	return len(qoderToolCallNamesFromRaw(message.Raw)) > 0
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
	p.commitFingerprints(p.committedFingerprints, usages...)
}

func (p *qoderConversationPlan) commitAccepted() {
	if p == nil {
		return
	}
	p.commitFingerprints(p.acceptedFingerprints())
	p.acceptedCommitted = true
}

func (p *qoderConversationPlan) rollbackAccepted() {
	if p == nil || !p.acceptedCommitted || p.store == nil || strings.TrimSpace(p.key) == "" {
		return
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	current := p.store.items[p.key]
	if current == nil {
		return
	}
	if current.sessionID != p.sessionID ||
		current.systemFingerprint != p.systemFingerprint ||
		current.toolsFingerprint != p.toolsFingerprint {
		return
	}
	if p.previousState == nil {
		delete(p.store.items, p.key)
		return
	}
	p.store.items[p.key] = cloneQoderConversationState(p.previousState)
}

func cloneQoderConversationState(state *qoderConversationState) *qoderConversationState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.messageFingerprints = append([]string(nil), state.messageFingerprints...)
	return &cloned
}

func (p *qoderConversationPlan) acceptedFingerprints() []string {
	if p == nil {
		return nil
	}
	if !p.reused {
		return append([]string(nil), p.committedFingerprints...)
	}
	prefixLen := p.prefixMessageCount
	if prefixLen < 0 {
		prefixLen = 0
	}
	if prefixLen > len(p.messageFingerprints) {
		prefixLen = len(p.messageFingerprints)
	}
	return append([]string(nil), p.messageFingerprints[:prefixLen]...)
}

func (p *qoderConversationPlan) commitFingerprints(fingerprints []string, usages ...ClaudeUsage) {
	if p == nil || p.store == nil || strings.TrimSpace(p.key) == "" {
		return
	}
	hasUsage, lastUsageInput, lastUsageOutput := p.previousUsageSnapshot()
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
	committedFingerprints := append([]string(nil), fingerprints...)
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

func (p *qoderConversationPlan) previousUsageSnapshot() (bool, int, int) {
	if p == nil {
		return false, 0, 0
	}
	hasUsage := p.hasPreviousUsage
	lastUsageInput := p.previousUsageInput
	lastUsageOutput := p.previousUsageOutput
	if p.store == nil || strings.TrimSpace(p.key) == "" {
		return hasUsage, lastUsageInput, lastUsageOutput
	}
	if existing := p.store.items[p.key]; existing != nil &&
		existing.sessionID == p.sessionID &&
		existing.systemFingerprint == p.systemFingerprint &&
		existing.toolsFingerprint == p.toolsFingerprint &&
		existing.hasUsage {
		hasUsage = true
		if existing.lastUsageInput > lastUsageInput {
			lastUsageInput = existing.lastUsageInput
		}
		if existing.lastUsageOutput > lastUsageOutput {
			lastUsageOutput = existing.lastUsageOutput
		}
	}
	return hasUsage, lastUsageInput, lastUsageOutput
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
	return upstreamUsage
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
	return diag.sentMessages < diag.originalMessages || diag.sentToolsBytes < diag.originalToolsBytes || diag.sentSystemBytes < diag.systemBytes
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
	if parsed := ParseMetadataUserID(request.metadataUserID); parsed != nil && strings.TrimSpace(parsed.SessionID) != "" {
		return "metadata_user_id:" + isolateOpenAISessionID(apiKeyID, parsed.SessionID), "metadata_user_id"
	}
	if value := qoderHeaderSessionID(c); value != "" {
		return "header:" + isolateOpenAISessionID(apiKeyID, value), "header"
	}
	if c != nil && c.Request != nil && IsClaudeCodeClient(c.Request.Context()) {
		if value := qoderClaudeCodeStablePrefixKey(request); value != "" {
			return "claude_code_prefix:" + isolateOpenAISessionID(apiKeyID, value), "claude_code_prefix"
		}
	}
	return "request:" + uuid.NewString(), "request"
}

func qoderClaudeCodeStablePrefixKey(request qoderPayloadRequest) string {
	parts := []string{
		"model=" + strings.TrimSpace(request.model),
		"system=" + qoderNormalizeSystemForFingerprint(request.system),
		"tools=" + qoderFingerprintAny(request.tools),
	}
	if firstUser := qoderFirstUserMessageText(request.messages); firstUser != "" {
		parts = append(parts, "first_user="+firstUser)
	}
	return strings.Join(parts, "|")
}

func qoderFirstUserMessageText(messages []qoderMessage) string {
	for _, message := range messages {
		if strings.TrimSpace(message.Role) != "user" {
			continue
		}
		if text := strings.TrimSpace(message.Text); text != "" {
			return text
		}
	}
	return ""
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
	prompt := latestQoderPayloadPromptText(messages, includeTools)
	if prompt == "" {
		prompt = latestQoderPayloadPromptText(request.messages, includeTools)
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
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	parameters["max_tokens"] = request.maxTokens
	modelConfig["key"] = modelInfo.Key
	modelConfig["source"] = modelInfo.Source
	chatContext, _ := payload["chat_context"].(map[string]any)
	chatText, _ := chatContext["text"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	extraModelConfig, _ := extra["modelConfig"].(map[string]any)
	extraOriginalContent, _ := extra["originalContent"].(map[string]any)
	chatText["text"] = prompt
	extraModelConfig["key"] = modelInfo.Key
	extraModelConfig["source"] = modelInfo.Source
	extraOriginalContent["text"] = prompt
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
	addQoderEphemeralCacheControl(outMessages)
	payload["messages"] = outMessages
	if includeTools {
		payload["tools"] = request.tools
	} else {
		payload["tools"] = []any{}
	}
	return payload, modelInfo.Key
}

func addQoderEphemeralCacheControl(messages []any) {
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		contents, _ := message["contents"].([]any)
		for j := len(contents) - 1; j >= 0; j-- {
			block, ok := contents[j].(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			text, _ := block["text"].(string)
			if strings.TrimSpace(text) == "" {
				continue
			}
			if _, exists := block["cache_control"]; exists {
				return
			}
			block["cache_control"] = map[string]any{"type": "ephemeral"}
			return
		}
	}
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

func qoderRequestMap(body []byte) map[string]any {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return map[string]any{}
	}
	return req
}

func qoderChatSystemText(messages []apicompat.ChatMessage) string {
	parts := make([]string, 0)
	for _, message := range messages {
		if message.Role != "system" {
			continue
		}
		text := qoderChatContentText(message.Content)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func qoderChatContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []apicompat.ChatContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "text" && part.Text != "" {
				out = append(out, part.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func qoderMessagesFromChatCompletions(messages []apicompat.ChatMessage) ([]qoderMessage, error) {
	out := make([]qoderMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := qoderMessageFromChatCompletionsMessage(message)
		if err != nil {
			return nil, err
		}
		if converted.Role != "" {
			out = append(out, converted)
		}
	}
	return out, nil
}

func qoderMessageFromChatCompletionsMessage(message apicompat.ChatMessage) (qoderMessage, error) {
	role := strings.TrimSpace(message.Role)
	switch role {
	case "", "user":
		text := qoderChatContentText(message.Content)
		return qoderMessage{Role: "user", Text: text, Raw: map[string]any{"role": "user", "content": text}}, nil
	case "system", "developer":
		return qoderMessage{}, nil
	case "assistant":
		text := qoderChatContentText(message.Content)
		raw := map[string]any{"role": "assistant", "content": text}
		if toolCalls := qoderChatToolCalls(message.ToolCalls); len(toolCalls) > 0 {
			raw["tool_calls"] = toolCalls
		}
		return qoderMessage{Role: "assistant", Text: text, Raw: raw}, nil
	case "tool":
		text := qoderChatContentText(message.Content)
		if text == "" {
			text = "(empty)"
		}
		raw := map[string]any{
			"role":         "tool",
			"tool_call_id": message.ToolCallID,
			"content":      text,
		}
		if name := strings.TrimSpace(message.Name); name != "" {
			raw["name"] = name
		}
		return qoderMessage{Role: "tool", Text: text, ToolCallID: message.ToolCallID, Raw: raw}, nil
	case "function":
		text := qoderChatContentText(message.Content)
		if text == "" {
			text = "(empty)"
		}
		callID := strings.TrimSpace(message.Name)
		raw := map[string]any{
			"role":         "tool",
			"tool_call_id": callID,
			"content":      text,
		}
		if callID != "" {
			raw["name"] = callID
		}
		return qoderMessage{Role: "tool", Text: text, ToolCallID: callID, Raw: raw}, nil
	default:
		return qoderMessage{}, fmt.Errorf("unsupported chat message role: %s", role)
	}
}

func qoderChatToolCalls(toolCalls []apicompat.ChatToolCall) []any {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]any, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		arguments := toolCall.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		callType := strings.TrimSpace(toolCall.Type)
		if callType == "" {
			callType = "function"
		}
		out = append(out, map[string]any{
			"id":   toolCall.ID,
			"type": callType,
			"function": map[string]any{
				"name":      toolCall.Function.Name,
				"arguments": arguments,
			},
		})
	}
	return out
}

func qoderAnthropicSystemText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return qoderStripVolatileSystemText(text), nil
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", errors.New("unsupported system field")
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type: %v", block.Type)
		}
		if text := qoderStripVolatileSystemText(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func qoderStripVolatileSystemText(text string) string {
	return qoderClaudeBillingCCHRe.ReplaceAllString(text, "${1}<cch>${2}")
}

func qoderMessagesFromResponsesInput(raw json.RawMessage) ([]qoderMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []qoderMessage{{Role: "user", Text: text, Raw: map[string]any{"role": "user", "content": text}}}, nil
	}
	var items []apicompat.ResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse canonical responses input: %w", err)
	}
	messages := make([]qoderMessage, 0, len(items))
	var pendingToolCalls []any
	flushPendingToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		messages = append(messages, qoderMessage{
			Role: "assistant",
			Raw: map[string]any{
				"role":       "assistant",
				"tool_calls": pendingToolCalls,
			},
		})
		pendingToolCalls = nil
	}
	for _, item := range items {
		if item.Type == "function_call" {
			pendingToolCalls = append(pendingToolCalls, qoderToolCallMap(item.CallID, item.Name, item.Arguments))
			continue
		}
		flushPendingToolCalls()
		converted := qoderMessageFromResponsesInputItem(item)
		if converted.Role != "" {
			messages = append(messages, converted)
		}
	}
	flushPendingToolCalls()
	return messages, nil
}

func qoderMessageFromResponsesInputItem(item apicompat.ResponsesInputItem) qoderMessage {
	switch item.Type {
	case "function_call":
		raw := map[string]any{
			"role":       "assistant",
			"tool_calls": []any{qoderToolCallMap(item.CallID, item.Name, item.Arguments)},
		}
		return qoderMessage{Role: "assistant", Raw: raw}
	case "function_call_output":
		output := item.Output
		if output == "" {
			output = "(empty)"
		}
		return qoderMessage{
			Role:       "tool",
			Text:       output,
			ToolCallID: item.CallID,
			Raw: map[string]any{
				"role":         "tool",
				"tool_call_id": item.CallID,
				"content":      output,
			},
		}
	default:
		role := qoderResponsesRole(item.Role)
		if role == "" {
			return qoderMessage{}
		}
		text := qoderResponsesContentText(item.Content)
		if text == "" && item.Type == "message" && role != "assistant" {
			return qoderMessage{Role: role, Raw: map[string]any{"role": role}}
		}
		return qoderMessage{
			Role: role,
			Text: text,
			Raw:  map[string]any{"role": role, "content": text},
		}
	}
}

func qoderResponsesContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []apicompat.ResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "input_text", "output_text", "text":
				if part.Text != "" {
					out = append(out, part.Text)
				}
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func qoderMessagesFromAnthropicMessages(messages []apicompat.AnthropicMessage) ([]qoderMessage, error) {
	out := make([]qoderMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := qoderMessagesFromAnthropicMessage(message)
		if err != nil {
			return nil, err
		}
		out = append(out, converted...)
	}
	return out, nil
}

func qoderMessagesFromAnthropicMessage(message apicompat.AnthropicMessage) ([]qoderMessage, error) {
	if message.Role != "user" {
		text, raw, err := qoderAnthropicMessageTextAndRaw(message)
		if err != nil {
			return nil, err
		}
		return []qoderMessage{{Role: message.Role, Text: text, Raw: raw}}, nil
	}

	blocks, ok, err := qoderAnthropicContentBlocks(message.Content)
	if err != nil {
		return nil, err
	}
	if !ok {
		text := qoderAnthropicContentText(message.Content)
		return []qoderMessage{{Role: message.Role, Text: text, Raw: map[string]any{"role": message.Role, "content": text}}}, nil
	}

	var out []qoderMessage
	var textParts []string
	var textContent []map[string]any
	flushText := func() {
		text := strings.Join(nonEmptyStrings(textParts), "\n")
		contents := textContent
		textParts = nil
		textContent = nil
		if text == "" {
			return
		}
		out = append(out, qoderMessage{
			Role:        "user",
			Text:        text,
			Raw:         map[string]any{"role": "user", "content": text},
			TextContent: contents,
		})
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			textContent = append(textContent, qoderTextContentBlock(block.Text, block.CacheControl))
		case "tool_result":
			toolText := qoderAnthropicToolResultText(block.Content)
			if strings.TrimSpace(block.ToolUseID) == "" {
				if toolText != "" {
					textParts = append(textParts, toolText)
				}
				continue
			}
			flushText()
			if toolText != "" {
				out = append(out, qoderMessage{
					Role:       "tool",
					Text:       toolText,
					ToolCallID: block.ToolUseID,
					Raw: map[string]any{
						"role":         "tool",
						"tool_call_id": block.ToolUseID,
						"content":      toolText,
					},
				})
			}
		case "tool_use":
			// Qoder payload.py ignores tool_use when flattening message text.
		case "thinking", "redacted_thinking":
			// Qoder does not accept Anthropic thinking signatures in history.
		default:
			return nil, fmt.Errorf("unsupported content block type: %v", block.Type)
		}
	}
	flushText()
	if len(out) == 0 {
		return []qoderMessage{{Role: message.Role, Raw: map[string]any{"role": message.Role}}}, nil
	}
	return out, nil
}

func qoderAnthropicMessageTextAndRaw(message apicompat.AnthropicMessage) (string, map[string]any, error) {
	blocks, ok, err := qoderAnthropicContentBlocks(message.Content)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		text := qoderAnthropicContentText(message.Content)
		return text, map[string]any{"role": message.Role, "content": text}, nil
	}
	textParts := make([]string, 0, len(blocks))
	rawBlocks := make([]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
			rawBlocks = append(rawBlocks, map[string]any{"type": "text", "text": block.Text})
		case "tool_use":
			rawBlocks = append(rawBlocks, map[string]any{
				"type":  "tool_use",
				"id":    block.ID,
				"name":  block.Name,
				"input": qoderRawMessageOrDefault(block.Input, map[string]any{}),
			})
		case "thinking", "redacted_thinking":
			// Do not include thinking in Qoder content/tool history.
		default:
			return "", nil, fmt.Errorf("unsupported content block type: %v", block.Type)
		}
	}
	return strings.Join(nonEmptyStrings(textParts), "\n"), map[string]any{"role": message.Role, "content": rawBlocks}, nil
}

func qoderAnthropicContentBlocks(raw json.RawMessage) ([]apicompat.AnthropicContentBlock, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return nil, false, nil
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false, err
	}
	return blocks, true, nil
}

func qoderAnthropicContentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	blocks, ok, err := qoderAnthropicContentBlocks(raw)
	if err != nil || !ok {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "tool_result":
			parts = append(parts, qoderAnthropicToolResultText(block.Content))
		}
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func qoderAnthropicToolResultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

func qoderResponsesRole(role string) string {
	switch strings.TrimSpace(role) {
	case "developer", "system":
		return ""
	case "assistant":
		return "assistant"
	case "", "user":
		return "user"
	default:
		return role
	}
}

func qoderToolCallMap(id, name, arguments string) map[string]any {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	}
}

func qoderResponsesToolsToQoderTools(tools []apicompat.ResponsesTool) []any {
	if len(tools) == 0 {
		return []any{}
	}
	converted := make([]any, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		function := map[string]any{
			"name":       tool.Name,
			"parameters": qoderRawMessageOrDefault(tool.Parameters, map[string]any{"type": "object", "properties": map[string]any{}}),
		}
		if tool.Description != "" {
			function["description"] = tool.Description
		}
		if tool.Strict != nil {
			function["strict"] = *tool.Strict
		}
		converted = append(converted, map[string]any{
			"type":     "function",
			"function": function,
		})
	}
	return converted
}

func qoderDeclaredToolNameMapper(tools []any) qoderToolNameMapper {
	aliases := qoderDeclaredToolNameAliases(tools)
	if len(aliases) == 0 {
		return nil
	}
	return func(name string) string {
		key := qoderToolNameKey(name)
		if key == "" {
			return name
		}
		if mapped := aliases[key]; mapped != "" {
			return mapped
		}
		return name
	}
}

func qoderDeclaredToolNameAliases(tools []any) map[string]string {
	aliases := map[string]string{}
	for _, raw := range tools {
		name := qoderDeclaredToolName(raw)
		if name == "" {
			continue
		}
		for _, alias := range qoderToolNameAliases(name) {
			key := qoderToolNameKey(alias)
			if key != "" {
				aliases[key] = name
			}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func qoderDeclaredToolName(raw any) string {
	tool, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if name := strings.TrimSpace(qoderStringField(tool, "name")); name != "" {
		return name
	}
	function, _ := tool["function"].(map[string]any)
	return strings.TrimSpace(qoderStringField(function, "name"))
}

func qoderToolNameAliases(name string) []string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	aliases := []string{trimmed}
	switch strings.ToLower(trimmed) {
	case "bash":
		aliases = append(aliases, "Bash", "shell", "execute_bash")
	case "read":
		aliases = append(aliases, "Read")
	case "write":
		aliases = append(aliases, "Write")
	case "edit":
		aliases = append(aliases, "Edit")
	case "multiedit", "multi_edit":
		aliases = append(aliases, "MultiEdit", "multi_edit")
	case "grep":
		aliases = append(aliases, "Grep")
	case "glob":
		aliases = append(aliases, "Glob")
	case "ls":
		aliases = append(aliases, "LS", "Ls")
	case "todowrite", "todo_write":
		aliases = append(aliases, "TodoWrite", "todo_write")
	}
	return aliases
}

func qoderToolNameKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", ""))
}

func qoderRawMessageOrDefault(raw json.RawMessage, fallback any) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return fallback
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback
	}
	return value
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
	if len(message.TextContent) > 0 {
		contents := make([]any, 0, len(message.TextContent))
		for _, block := range message.TextContent {
			contents = append(contents, block)
		}
		msg["contents"] = contents
	} else if message.Text != "" {
		msg["contents"] = []any{map[string]any{"type": "text", "text": message.Text}}
	}
	copyQoderToolFields(msg, message.Raw)
	if strings.TrimSpace(message.ToolCallID) != "" {
		toolCallID := strings.TrimSpace(message.ToolCallID)
		msg["tool_call_id"] = toolCallID
		msg["tool_call_call_id"] = toolCallID
	}
	return msg
}

func qoderTextContentBlock(text string, cacheControl *apicompat.AnthropicCacheControl) map[string]any {
	block := map[string]any{"type": "text", "text": text}
	if cacheControl != nil {
		block["cache_control"] = map[string]any{"type": cacheControl.Type}
	}
	return block
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
	if toolCallID := firstNonEmptyQoder(
		qoderStringField(raw, "tool_call_id"),
		qoderStringField(raw, "tool_call_call_id"),
		qoderStringField(raw, "call_id"),
	); toolCallID != "" {
		out["tool_call_id"] = toolCallID
		out["tool_call_call_id"] = toolCallID
	}
	if name, ok := raw["name"].(string); ok && strings.TrimSpace(name) != "" {
		out["name"] = name
	}
}

func enrichQoderToolResultMessages(messages []qoderMessage) []qoderMessage {
	toolNamesByID := map[string]string{}
	for i := range messages {
		for id, name := range qoderToolCallNamesFromRaw(messages[i].Raw) {
			if id != "" && name != "" {
				toolNamesByID[id] = name
			}
		}
		if messages[i].Role != "tool" {
			continue
		}
		toolCallID := qoderMessageToolCallID(messages[i])
		if toolCallID == "" {
			continue
		}
		if strings.TrimSpace(messages[i].ToolCallID) == "" {
			messages[i].ToolCallID = toolCallID
		}
		if qoderStringField(messages[i].Raw, "name") != "" {
			continue
		}
		name := toolNamesByID[toolCallID]
		if name == "" {
			continue
		}
		if messages[i].Raw == nil {
			messages[i].Raw = map[string]any{}
		}
		messages[i].Raw["name"] = name
	}
	return messages
}

func qoderMessageToolCallID(message qoderMessage) string {
	return firstNonEmptyQoder(
		message.ToolCallID,
		qoderStringField(message.Raw, "tool_call_id"),
		qoderStringField(message.Raw, "tool_call_call_id"),
		qoderStringField(message.Raw, "call_id"),
	)
}

func qoderToolCallNamesFromRaw(raw map[string]any) map[string]string {
	names := map[string]string{}
	if len(raw) == 0 {
		return names
	}
	for _, rawTool := range qoderAnySlice(raw["tool_calls"]) {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		function, _ := tool["function"].(map[string]any)
		id := firstNonEmptyQoder(
			qoderStringField(tool, "id"),
			qoderStringField(tool, "tool_call_id"),
			qoderStringField(tool, "call_id"),
		)
		name := firstNonEmptyQoder(
			qoderStringField(function, "name"),
			qoderStringField(tool, "name"),
			qoderStringField(tool, "tool_name"),
		)
		if id != "" && name != "" {
			names[id] = name
		}
	}
	for _, rawBlock := range qoderAnySlice(raw["content"]) {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "tool_use" {
			continue
		}
		id := qoderStringField(block, "id")
		name := qoderStringField(block, "name")
		if id != "" && name != "" {
			names[id] = name
		}
	}
	return names
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

func ReadQoderSSEEvents(resp *http.Response) ([]qoder.SSEEvent, error) {
	return ReadQoderSSEEventsContext(context.Background(), resp, nil)
}

func ReadQoderSSEEventsContext(ctx context.Context, resp *http.Response, keepalive func() error) ([]qoder.SSEEvent, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("qoder response body is nil")
	}
	events := make([]qoder.SSEEvent, 0)
	if err := streamQoderEvents(ctx, resp, func(event qoder.SSEEvent) error {
		events = append(events, event)
		return nil
	}, keepalive); err != nil {
		return nil, err
	}
	return events, nil
}

func qoderNonStreamingKeepalive(c *gin.Context) func() error {
	if c == nil || c.Writer == nil {
		return nil
	}
	header := c.Writer.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	return func() error {
		if !c.Writer.Written() {
			c.Writer.WriteHeader(http.StatusOK)
		}
		// Non-streaming clients parse the final body as JSON; whitespace keeps
		// the connection alive without corrupting that JSON document.
		_, err := io.WriteString(c.Writer, "\n")
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}
}

func WriteQoderOpenAIStream(c *gin.Context, model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) error {
	events = normalizeQoderTextToolCallEvents(events)

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
	toolCalls := newQoderOpenAIToolCallAccumulator(toolNameMappers...)
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
			totalTokens = event.TotalTokens
			if err := writeSSEData(c.Writer, openAIUsageChunk(completionID, model, usage, totalTokens, event.UsageDetails)); err != nil {
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
			deltas := toolCalls.AppendDelta(event)
			if len(deltas) == 0 {
				continue
			}
			if err := writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"tool_calls": deltas}, nil)); err != nil {
				return err
			}
		}
	}
	return nil
}

type qoderStreamResult struct {
	Usage        ClaudeUsage
	UsageDetails qoder.UsageDetails
	TotalTokens  int
	HasOutput    bool
}

type qoderToolNameMapper func(string) string

type qoderOpenAIStreamResponseOption struct {
	mapUsage    func(ClaudeUsage) ClaudeUsage
	mapToolName qoderToolNameMapper
}

type qoderAnthropicStreamResponseOption struct {
	mapUsage    func(ClaudeUsage) ClaudeUsage
	mapToolName qoderToolNameMapper
}

type qoderResponsesStreamResponseOption struct {
	mapUsage    func(ClaudeUsage) ClaudeUsage
	mapToolName qoderToolNameMapper
}

func qoderOpenAIStreamUsageMapper(mapper func(ClaudeUsage) ClaudeUsage) qoderOpenAIStreamResponseOption {
	return qoderOpenAIStreamResponseOption{mapUsage: mapper}
}

func qoderOpenAIStreamToolNameMapper(mapper qoderToolNameMapper) qoderOpenAIStreamResponseOption {
	return qoderOpenAIStreamResponseOption{mapToolName: mapper}
}

func qoderAnthropicStreamUsageMapper(mapper func(ClaudeUsage) ClaudeUsage) qoderAnthropicStreamResponseOption {
	return qoderAnthropicStreamResponseOption{mapUsage: mapper}
}

func qoderAnthropicStreamToolNameMapper(mapper qoderToolNameMapper) qoderAnthropicStreamResponseOption {
	return qoderAnthropicStreamResponseOption{mapToolName: mapper}
}

func qoderResponsesStreamUsageMapper(mapper func(ClaudeUsage) ClaudeUsage) qoderResponsesStreamResponseOption {
	return qoderResponsesStreamResponseOption{mapUsage: mapper}
}

func qoderResponsesStreamToolNameMapper(mapper qoderToolNameMapper) qoderResponsesStreamResponseOption {
	return qoderResponsesStreamResponseOption{mapToolName: mapper}
}

func qoderOpenAIStreamResponseOptions(options []qoderOpenAIStreamResponseOption) (func(ClaudeUsage) ClaudeUsage, qoderToolNameMapper) {
	var usageMapper func(ClaudeUsage) ClaudeUsage
	var toolNameMapper qoderToolNameMapper
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
	}
	return usageMapper, toolNameMapper
}

func qoderAnthropicStreamResponseOptions(options []qoderAnthropicStreamResponseOption) (func(ClaudeUsage) ClaudeUsage, qoderToolNameMapper) {
	var usageMapper func(ClaudeUsage) ClaudeUsage
	var toolNameMapper qoderToolNameMapper
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
	}
	return usageMapper, toolNameMapper
}

func qoderResponsesStreamResponseOptions(options []qoderResponsesStreamResponseOption) (func(ClaudeUsage) ClaudeUsage, qoderToolNameMapper) {
	var usageMapper func(ClaudeUsage) ClaudeUsage
	var toolNameMapper qoderToolNameMapper
	for _, option := range options {
		if option.mapUsage != nil && usageMapper == nil {
			usageMapper = option.mapUsage
		}
		if option.mapToolName != nil && toolNameMapper == nil {
			toolNameMapper = option.mapToolName
		}
	}
	return usageMapper, toolNameMapper
}

type qoderTextToolCallTransformer struct {
	buffer       string
	nextToolCall int
}

func newQoderTextToolCallTransformer() *qoderTextToolCallTransformer {
	return &qoderTextToolCallTransformer{}
}

func normalizeQoderTextToolCallEvents(events []qoder.SSEEvent) []qoder.SSEEvent {
	if len(events) == 0 {
		return events
	}
	transformer := newQoderTextToolCallTransformer()
	out := make([]qoder.SSEEvent, 0, len(events))
	for _, event := range events {
		out = append(out, transformer.Append(event)...)
	}
	out = append(out, transformer.Flush()...)
	return out
}

func (t *qoderTextToolCallTransformer) Append(event qoder.SSEEvent) []qoder.SSEEvent {
	if t == nil {
		return []qoder.SSEEvent{event}
	}
	if event.Type == "text_delta" {
		if event.Text == "" {
			return nil
		}
		t.buffer += event.Text
		return t.drain(false)
	}
	out := t.drain(true)
	out = append(out, event)
	return out
}

func (t *qoderTextToolCallTransformer) Flush() []qoder.SSEEvent {
	if t == nil {
		return nil
	}
	return t.drain(true)
}

func (t *qoderTextToolCallTransformer) drain(final bool) []qoder.SSEEvent {
	out := make([]qoder.SSEEvent, 0)
	for t.buffer != "" {
		start, marker := qoderTextToolCallMarkerStart(t.buffer)
		if start < 0 {
			if final {
				out = append(out, qoder.SSEEvent{Type: "text_delta", Text: t.buffer})
				t.buffer = ""
				return out
			}
			suffixLen := qoderToolCallStartSuffixLen(t.buffer)
			text := t.buffer
			if suffixLen > 0 {
				text = t.buffer[:len(t.buffer)-suffixLen]
				t.buffer = t.buffer[len(t.buffer)-suffixLen:]
			} else {
				t.buffer = ""
			}
			if text != "" {
				out = append(out, qoder.SSEEvent{Type: "text_delta", Text: text})
			}
			return out
		}
		if start > 0 {
			out = append(out, qoder.SSEEvent{Type: "text_delta", Text: t.buffer[:start]})
			t.buffer = t.buffer[start:]
			continue
		}

		startTag, endTag := qoderTextToolCallMarkerTags(marker)
		end := strings.Index(t.buffer[len(startTag):], endTag)
		if end < 0 {
			if final {
				out = append(out, qoder.SSEEvent{Type: "text_delta", Text: t.buffer})
				t.buffer = ""
			}
			return out
		}
		segmentEnd := len(startTag) + end + len(endTag)
		segment := t.buffer[:segmentEnd]
		if events, ok := t.parseToolCallSegment(segment, marker); ok {
			out = append(out, events...)
		} else {
			out = append(out, qoder.SSEEvent{Type: "text_delta", Text: segment})
		}
		t.buffer = t.buffer[segmentEnd:]
	}
	return out
}

func qoderTextToolCallMarkerStart(text string) (int, string) {
	xmlStart := strings.Index(text, qoderTextToolCallStart)
	dsmlStart := strings.Index(text, qoderDSMLToolCallsStart)
	if xmlStart < 0 {
		return dsmlStart, qoderDSMLToolCallsStart
	}
	if dsmlStart < 0 || xmlStart < dsmlStart {
		return xmlStart, qoderTextToolCallStart
	}
	return dsmlStart, qoderDSMLToolCallsStart
}

func qoderTextToolCallMarkerTags(marker string) (string, string) {
	if marker == qoderDSMLToolCallsStart {
		return qoderDSMLToolCallsStart, qoderDSMLToolCallsEnd
	}
	return qoderTextToolCallStart, qoderTextToolCallEnd
}

func qoderToolCallStartSuffixLen(text string) int {
	maxSuffix := 0
	for _, marker := range []string{qoderTextToolCallStart, qoderDSMLToolCallsStart} {
		limit := min(len(marker)-1, len(text))
		for i := limit; i > 0; i-- {
			if strings.HasPrefix(marker, text[len(text)-i:]) && i > maxSuffix {
				maxSuffix = i
			}
		}
	}
	return maxSuffix
}

func (t *qoderTextToolCallTransformer) parseToolCallSegment(segment string, marker string) ([]qoder.SSEEvent, bool) {
	if marker == qoderDSMLToolCallsStart {
		return t.parseDSMLToolCalls(segment)
	}
	event, ok := t.parseToolCall(segment)
	if !ok {
		return nil, false
	}
	return []qoder.SSEEvent{event}, true
}

func (t *qoderTextToolCallTransformer) parseToolCall(segment string) (qoder.SSEEvent, bool) {
	inner := strings.TrimPrefix(segment, qoderTextToolCallStart)
	inner = strings.TrimSuffix(inner, qoderTextToolCallEnd)
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return qoder.SSEEvent{}, false
	}
	if event, ok := t.parseJSONToolCall(inner, segment); ok {
		return event, true
	}

	firstArgTag := len(inner)
	for _, tag := range []string{qoderTextArgValueStart, qoderTextArgKeyStart} {
		if idx := strings.Index(inner, tag); idx >= 0 && idx < firstArgTag {
			firstArgTag = idx
		}
	}
	name := strings.TrimSpace(html.UnescapeString(inner[:firstArgTag]))
	if name == "" {
		return qoder.SSEEvent{}, false
	}

	args := map[string]any{}
	rest := inner[firstArgTag:]
	for {
		keyOpen := strings.Index(rest, qoderTextArgKeyStart)
		if keyOpen < 0 {
			break
		}
		keyStart := keyOpen + len(qoderTextArgKeyStart)
		keyEnd := strings.Index(rest[keyStart:], qoderTextArgKeyEnd)
		if keyEnd < 0 {
			return qoder.SSEEvent{}, false
		}
		key := strings.TrimSpace(html.UnescapeString(rest[keyStart : keyStart+keyEnd]))
		afterKey := rest[keyStart+keyEnd+len(qoderTextArgKeyEnd):]

		valueOpen := strings.Index(afterKey, qoderTextArgValueStart)
		if valueOpen < 0 {
			return qoder.SSEEvent{}, false
		}
		valueStart := valueOpen + len(qoderTextArgValueStart)
		valueEnd := strings.Index(afterKey[valueStart:], qoderTextArgValueEnd)
		if valueEnd < 0 {
			return qoder.SSEEvent{}, false
		}
		if key != "" {
			args[key] = html.UnescapeString(afterKey[valueStart : valueStart+valueEnd])
		}
		rest = afterKey[valueStart+valueEnd+len(qoderTextArgValueEnd):]
	}

	arguments, err := json.Marshal(args)
	if err != nil {
		return qoder.SSEEvent{}, false
	}
	index := t.nextToolCall
	id := qoderTextToolCallID(index, segment)
	t.nextToolCall++
	return qoder.SSEEvent{
		Type:             "tool_call_delta",
		ToolCallID:       id,
		ToolCallIndex:    index,
		HasToolCallIndex: true,
		ToolType:         "function",
		ToolName:         name,
		Arguments:        string(arguments),
	}, true
}

func (t *qoderTextToolCallTransformer) parseJSONToolCall(inner string, segment string) (qoder.SSEEvent, bool) {
	decodedText := strings.TrimSpace(html.UnescapeString(inner))
	if !strings.HasPrefix(decodedText, "{") {
		return qoder.SSEEvent{}, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(decodedText), &payload); err != nil {
		return qoder.SSEEvent{}, false
	}
	function, _ := payload["function"].(map[string]any)
	name := firstNonEmptyQoder(
		qoderStringField(function, "name"),
		qoderStringField(payload, "name"),
		qoderStringField(payload, "tool"),
		qoderStringField(payload, "tool_name"),
	)
	if name == "" {
		return qoder.SSEEvent{}, false
	}
	arguments := firstPresentQoderToolArguments(
		function["arguments"],
		payload["arguments"],
		payload["input"],
		payload["parameters"],
		qoderInlineToolArguments(payload),
	)
	name, arguments = normalizeQoderTextToolCallNameAndArguments(name, arguments)
	index := t.nextToolCall
	id := firstNonEmptyQoder(
		qoderStringField(payload, "id"),
		qoderStringField(payload, "tool_call_id"),
		qoderStringField(payload, "call_id"),
		qoderTextToolCallID(index, segment),
	)
	t.nextToolCall++
	return qoder.SSEEvent{
		Type:             "tool_call_delta",
		ToolCallID:       id,
		ToolCallIndex:    index,
		HasToolCallIndex: true,
		ToolType:         firstNonEmptyQoder(qoderStringField(payload, "type"), "function"),
		ToolName:         name,
		Arguments:        arguments,
	}, true
}

func firstPresentQoderToolArguments(values ...any) string {
	for _, value := range values {
		if value == nil {
			continue
		}
		arguments := qoderToolArgumentsString(value)
		if strings.TrimSpace(arguments) != "" {
			return arguments
		}
	}
	return "{}"
}

func qoderInlineToolArguments(payload map[string]any) any {
	if len(payload) == 0 {
		return nil
	}
	args := map[string]any{}
	for _, key := range []string{"command", "cmd", "description"} {
		if value, ok := payload[key]; ok {
			args[key] = value
		}
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

func normalizeQoderTextToolCallNameAndArguments(name string, arguments string) (string, string) {
	originalName := strings.TrimSpace(name)
	normalizedName := originalName
	if strings.EqualFold(originalName, "shell") || strings.EqualFold(originalName, "execute_bash") {
		normalizedName = "Bash"
	}
	if normalizedName != "Bash" {
		return normalizedName, arguments
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		command := strings.TrimSpace(arguments)
		if command == "" || command == "{}" {
			return normalizedName, "{}"
		}
		if strings.HasPrefix(command, "{") || strings.HasPrefix(command, "[") {
			return normalizedName, arguments
		}
		body, _ := json.Marshal(map[string]any{"command": command})
		return normalizedName, string(body)
	}
	if _, ok := args["command"]; !ok {
		if cmd, ok := args["cmd"]; ok {
			args["command"] = cmd
			delete(args, "cmd")
		}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return normalizedName, arguments
	}
	return normalizedName, string(body)
}

func (t *qoderTextToolCallTransformer) parseDSMLToolCalls(segment string) ([]qoder.SSEEvent, bool) {
	inner := strings.TrimPrefix(segment, qoderDSMLToolCallsStart)
	inner = strings.TrimSuffix(inner, qoderDSMLToolCallsEnd)
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil, false
	}
	events := make([]qoder.SSEEvent, 0)
	rest := inner
	for {
		start := strings.Index(rest, qoderDSMLInvokeStart)
		if start < 0 {
			break
		}
		rest = rest[start:]
		openEnd := strings.Index(rest, ">")
		if openEnd < 0 {
			return nil, false
		}
		closeStart := strings.Index(rest[openEnd+1:], qoderDSMLInvokeEnd)
		if closeStart < 0 {
			return nil, false
		}
		openTag := rest[:openEnd+1]
		body := rest[openEnd+1 : openEnd+1+closeStart]
		if event, ok := t.parseDSMLInvoke(openTag, body, segment); ok {
			events = append(events, event)
		}
		rest = rest[openEnd+1+closeStart+len(qoderDSMLInvokeEnd):]
	}
	return events, len(events) > 0
}

func (t *qoderTextToolCallTransformer) parseDSMLInvoke(openTag string, body string, segment string) (qoder.SSEEvent, bool) {
	name := qoderTagAttribute(openTag, "name")
	if name == "" {
		return qoder.SSEEvent{}, false
	}
	args := map[string]any{}
	rest := body
	for {
		start := strings.Index(rest, qoderDSMLParameterStart)
		if start < 0 {
			break
		}
		rest = rest[start:]
		openEnd := strings.Index(rest, ">")
		if openEnd < 0 {
			return qoder.SSEEvent{}, false
		}
		closeStart := strings.Index(rest[openEnd+1:], qoderDSMLParameterEnd)
		if closeStart < 0 {
			return qoder.SSEEvent{}, false
		}
		openParam := rest[:openEnd+1]
		paramName := qoderTagAttribute(openParam, "name")
		if paramName != "" {
			args[paramName] = html.UnescapeString(rest[openEnd+1 : openEnd+1+closeStart])
		}
		rest = rest[openEnd+1+closeStart+len(qoderDSMLParameterEnd):]
	}
	if len(args) == 0 {
		return qoder.SSEEvent{}, false
	}
	arguments, err := json.Marshal(args)
	if err != nil {
		return qoder.SSEEvent{}, false
	}
	name, normalizedArguments := normalizeQoderTextToolCallNameAndArguments(name, string(arguments))
	index := t.nextToolCall
	t.nextToolCall++
	return qoder.SSEEvent{
		Type:             "tool_call_delta",
		ToolCallID:       qoderTextToolCallID(index, segment),
		ToolCallIndex:    index,
		HasToolCallIndex: true,
		ToolType:         "function",
		ToolName:         name,
		Arguments:        normalizedArguments,
	}, true
}

func qoderTagAttribute(tag string, name string) string {
	pattern := name + `="`
	start := strings.Index(tag, pattern)
	if start < 0 {
		return ""
	}
	start += len(pattern)
	end := strings.Index(tag[start:], `"`)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(tag[start : start+end]))
}

func normalizeQoderOutboundToolCallEvent(event qoder.SSEEvent) qoder.SSEEvent {
	if strings.TrimSpace(event.ToolName) == "" {
		return event
	}
	event.ToolName, event.Arguments = normalizeQoderTextToolCallNameAndArguments(event.ToolName, event.Arguments)
	return event
}

func qoderTextToolCallID(index int, segment string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d\n%s", index, segment)))
	return fmt.Sprintf("call_%x", sum[:12])
}

type qoderOpenAIToolCallAccumulator struct {
	calls               []qoderOpenAIToolCallState
	slotByUpstreamIndex map[int]int
	mapToolName         qoderToolNameMapper
}

type qoderOpenAIToolCallState struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

func newQoderOpenAIToolCallAccumulator(toolNameMappers ...qoderToolNameMapper) *qoderOpenAIToolCallAccumulator {
	var mapper qoderToolNameMapper
	for _, candidate := range toolNameMappers {
		if candidate != nil {
			mapper = candidate
			break
		}
	}
	return &qoderOpenAIToolCallAccumulator{mapToolName: mapper}
}

func (a *qoderOpenAIToolCallAccumulator) AppendDelta(event qoder.SSEEvent) []any {
	if a == nil {
		return []any{}
	}
	event = normalizeQoderOutboundToolCallEvent(event)
	if qoderToolCallDeltaIsEmptyPlaceholder(event) {
		return []any{}
	}
	if event.ToolCallID == "" && !event.HasToolCallIndex && event.ToolName == "" && event.ToolType == "" && event.Arguments != "" && len(a.calls) > 1 {
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
		state.Name = a.toolName(event.ToolName)
	}
	if event.Arguments != "" {
		state.Arguments = mergeQoderToolArguments(state.Arguments, event.Arguments)
	}
	a.bindUpstreamIndex(event, index)
	return []any{qoderOpenAIToolCallDelta(index, a.mapEventToolName(event))}
}

func (a *qoderOpenAIToolCallAccumulator) toolName(name string) string {
	if a == nil || a.mapToolName == nil || strings.TrimSpace(name) == "" {
		return name
	}
	mapped := strings.TrimSpace(a.mapToolName(name))
	if mapped == "" {
		return name
	}
	return mapped
}

func (a *qoderOpenAIToolCallAccumulator) mapEventToolName(event qoder.SSEEvent) qoder.SSEEvent {
	if event.ToolName != "" {
		event.ToolName = a.toolName(event.ToolName)
	}
	return event
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
				if a.shouldStartNewToolCallInSlot(event, index) {
					return len(a.calls)
				}
				return index
			}
			if event.ToolCallID != "" && a.calls[index].ID != "" && a.calls[index].ID != event.ToolCallID {
				return len(a.calls)
			}
		}
	}
	if event.HasToolCallIndex && event.ToolCallIndex >= 0 && event.ToolCallID == "" && (event.ToolName != "" || event.ToolType != "") {
		if event.ToolCallIndex < len(a.calls) && a.shouldStartNewToolCallInSlot(event, event.ToolCallIndex) {
			return len(a.calls)
		}
		return event.ToolCallIndex
	}
	if a.shouldStartImplicitToolCall(event) {
		return len(a.calls)
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

func (a *qoderOpenAIToolCallAccumulator) shouldStartImplicitToolCall(event qoder.SSEEvent) bool {
	if a == nil || len(a.calls) == 0 || event.ToolCallID != "" || event.HasToolCallIndex {
		return false
	}
	return a.shouldStartNewToolCallInSlot(event, len(a.calls)-1)
}

func (a *qoderOpenAIToolCallAccumulator) shouldStartNewToolCallInSlot(event qoder.SSEEvent, index int) bool {
	if a == nil || index < 0 || index >= len(a.calls) || event.ToolCallID != "" {
		return false
	}
	if event.ToolName == "" && event.ToolType == "" {
		return false
	}
	existing := a.calls[index]
	if existing.ID == "" && existing.Name == "" && existing.Arguments == "" {
		return false
	}
	if qoderToolArgumentsAreCompleteJSON(existing.Arguments) && !qoderToolArgumentStringIsEmptyPlaceholder(existing.Arguments) {
		return true
	}
	return existing.Name != "" && existing.Arguments == "" && event.Arguments == ""
}

func qoderToolArgumentsAreCompleteJSON(arguments string) bool {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return false
	}
	return json.Valid([]byte(trimmed))
}

func mergeQoderToolArguments(existing string, delta string) string {
	if delta == "" {
		return existing
	}
	if existing == "" {
		return delta
	}
	if strings.TrimSpace(delta) == "{}" {
		return existing
	}
	if strings.TrimSpace(existing) == "{}" {
		return delta
	}
	return existing + delta
}

func qoderToolCallDeltaIsEmptyPlaceholder(event qoder.SSEEvent) bool {
	return event.ToolCallID == "" && event.ToolName == "" && event.Arguments == "" && event.ToolType != ""
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
			a.slotByUpstreamIndex[event.ToolCallIndex] = slot
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
	if event.Arguments != "" && !qoderToolArgumentDeltaIsEmptyPlaceholder(event) {
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

func qoderToolArgumentDeltaIsEmptyPlaceholder(event qoder.SSEEvent) bool {
	return qoderToolArgumentStringIsEmptyPlaceholder(event.Arguments)
}

func qoderToolArgumentStringIsEmptyPlaceholder(arguments string) bool {
	return strings.TrimSpace(arguments) == "{}"
}

func WriteQoderOpenAIStreamResponse(ctx context.Context, c *gin.Context, model string, resp *http.Response, options ...qoderOpenAIStreamResponseOption) (*qoderStreamResult, error) {
	usageMapper, toolNameMapper := qoderOpenAIStreamResponseOptions(options)
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
	toolCalls := newQoderOpenAIToolCallAccumulator(toolNameMapper)
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
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
	if err := streamQoderEvents(ctx, resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			result.TotalTokens = event.TotalTokens
			result.UsageDetails = event.UsageDetails
			chunkUsage := result.Usage
			if usageMapper != nil {
				chunkUsage = usageMapper(chunkUsage)
			}
			totalTokens := result.TotalTokens
			if totalTokens == 0 {
				totalTokens = chunkUsage.InputTokens + chunkUsage.CacheReadInputTokens + chunkUsage.OutputTokens
			}
			return writeSSEData(c.Writer, openAIUsageChunk(completionID, model, chunkUsage, totalTokens, event.UsageDetails))
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			result.HasOutput = true
			return writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"content": event.Text}, nil))
		}
		if event.Type == "tool_call_delta" {
			result.HasOutput = true
			deltas := toolCalls.AppendDelta(event)
			if len(deltas) == 0 {
				return nil
			}
			return writeSSEData(c.Writer, openAIChunk(completionID, model, map[string]any{"tool_calls": deltas}, nil))
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
	if err := finish(); err != nil {
		return nil, err
	}
	return result, nil
}

func WriteQoderAnthropicStream(c *gin.Context, model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) error {
	events = normalizeQoderTextToolCallEvents(events)

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
	writer := newQoderAnthropicContentWriter(c.Writer, toolNameMappers...)
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := writer.closeOpenBlock(); err != nil {
			return err
		}
		if err := writer.ensureContentBlock(); err != nil {
			return err
		}
		if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   writer.stopReason(),
				"stop_sequence": nil,
			},
			"usage": qoderAnthropicUsage(usage, qoderUsageDetailsFromEvents(events)),
		}); err != nil {
			return err
		}
		return writeAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
	}
	for _, event := range events {
		if event.HasUsage {
			mergeQoderUsageEvent(&usage, event)
			continue
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			if err := writer.writeTextDelta(event.Text); err != nil {
				return err
			}
			continue
		}
		if event.Type == "reasoning_delta" {
			if err := writer.writeThinkingDelta(event.Text); err != nil {
				return err
			}
			continue
		}
		if event.Type == "tool_call_delta" {
			if err := writer.writeToolCall(event); err != nil {
				return err
			}
		}
	}
	if err := finish(); err != nil {
		return err
	}
	return nil
}

func WriteQoderAnthropicStreamResponse(ctx context.Context, c *gin.Context, model string, resp *http.Response, options ...qoderAnthropicStreamResponseOption) (*qoderStreamResult, error) {
	usageMapper, toolNameMapper := qoderAnthropicStreamResponseOptions(options)
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
	writer := newQoderAnthropicContentWriter(c.Writer, toolNameMapper)
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := writer.closeOpenBlock(); err != nil {
			return err
		}
		if err := writer.ensureContentBlock(); err != nil {
			return err
		}
		finalUsage := result.Usage
		if usageMapper != nil {
			finalUsage = usageMapper(finalUsage)
		}
		if err := writeAnthropicSSE(c.Writer, "message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   writer.stopReason(),
				"stop_sequence": nil,
			},
			"usage": qoderAnthropicUsage(finalUsage, result.UsageDetails),
		}); err != nil {
			return err
		}
		return writeAnthropicSSE(c.Writer, "message_stop", map[string]any{"type": "message_stop"})
	}
	if err := streamQoderEvents(ctx, resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			result.UsageDetails = event.UsageDetails
			return nil
		}
		if event.IsDone {
			return finish()
		}
		if event.Type == "text_delta" && event.Text != "" {
			result.HasOutput = true
			return writer.writeTextDelta(event.Text)
		}
		if event.Type == "reasoning_delta" {
			if event.Text != "" {
				result.HasOutput = true
			}
			return writer.writeThinkingDelta(event.Text)
		}
		if event.Type == "tool_call_delta" {
			result.HasOutput = true
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
	if err := finish(); err != nil {
		return nil, err
	}
	return result, nil
}

func BuildQoderResponsesResponse(model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	anthropicBody, err := BuildQoderAnthropicMessage(model, events, toolNameMappers...)
	if err != nil {
		return nil, err
	}
	var anthropicResp apicompat.AnthropicResponse
	if err := json.Unmarshal(anthropicBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("parse qoder anthropic response: %w", err)
	}
	responsesResp := apicompat.AnthropicToResponsesResponse(&anthropicResp)
	responsesResp.Model = model
	return json.Marshal(responsesResp)
}

func WriteQoderResponsesStreamResponse(ctx context.Context, c *gin.Context, model string, resp *http.Response, options ...qoderResponsesStreamResponseOption) (*qoderStreamResult, error) {
	usageMapper, toolNameMapper := qoderResponsesStreamResponseOptions(options)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	result := &qoderStreamResult{}
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	sequence := 0
	writeEvent := func(evt apicompat.ResponsesStreamEvent) error {
		evt.SequenceNumber = sequence
		sequence++
		sse, err := apicompat.ResponsesEventToSSE(evt)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(c.Writer, sse); err != nil {
			return err
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	if err := writeEvent(apicompat.ResponsesStreamEvent{
		Type: "response.created",
		Response: &apicompat.ResponsesResponse{
			ID:     responseID,
			Object: "response",
			Model:  model,
			Status: "in_progress",
			Output: []apicompat.ResponsesOutput{},
		},
	}); err != nil {
		closeQoderResponse(resp)
		return nil, err
	}

	nextOutputIndex := 0
	var messageItemID string
	messageOutputIndex := -1
	messageOpen := false
	messageDone := false
	var messageText strings.Builder
	completedOutputs := map[int]apicompat.ResponsesOutput{}
	completedOutputList := func() []apicompat.ResponsesOutput {
		if len(completedOutputs) == 0 {
			return []apicompat.ResponsesOutput{}
		}
		indexes := make([]int, 0, len(completedOutputs))
		for index := range completedOutputs {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		outputs := make([]apicompat.ResponsesOutput, 0, len(indexes))
		for _, index := range indexes {
			outputs = append(outputs, completedOutputs[index])
		}
		return outputs
	}
	openMessage := func() error {
		if messageOpen || messageDone {
			return nil
		}
		messageItemID = "item_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		messageOutputIndex = nextOutputIndex
		nextOutputIndex++
		messageOpen = true
		return writeEvent(apicompat.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: messageOutputIndex,
			Item: &apicompat.ResponsesOutput{
				Type:   "message",
				ID:     messageItemID,
				Role:   "assistant",
				Status: "in_progress",
			},
		})
	}
	closeMessage := func() error {
		if !messageOpen || messageDone {
			return nil
		}
		if err := writeEvent(apicompat.ResponsesStreamEvent{
			Type:         "response.output_text.done",
			OutputIndex:  messageOutputIndex,
			ContentIndex: 0,
			ItemID:       messageItemID,
			Text:         messageText.String(),
		}); err != nil {
			return err
		}
		item := apicompat.ResponsesOutput{
			Type:    "message",
			ID:      messageItemID,
			Role:    "assistant",
			Content: []apicompat.ResponsesContentPart{{Type: "output_text", Text: messageText.String()}},
			Status:  "completed",
		}
		if err := writeEvent(apicompat.ResponsesStreamEvent{
			Type:        "response.output_item.done",
			OutputIndex: messageOutputIndex,
			Item:        &item,
		}); err != nil {
			return err
		}
		completedOutputs[messageOutputIndex] = item
		messageOpen = false
		messageDone = true
		return nil
	}

	toolAccumulator := newQoderOpenAIToolCallAccumulator(toolNameMapper)
	toolStates := map[int]*qoderResponsesStreamToolState{}
	toolIndexForEvent := func(event qoder.SSEEvent) int {
		normalized := normalizeQoderOutboundToolCallEvent(event)
		return toolAccumulator.resolveIndex(normalized)
	}
	ensureToolAdded := func(index int) (*qoderResponsesStreamToolState, error) {
		for len(toolAccumulator.calls) <= index {
			toolAccumulator.calls = append(toolAccumulator.calls, qoderOpenAIToolCallState{Type: "function"})
		}
		call := toolAccumulator.calls[index]
		state := toolStates[index]
		if state == nil {
			state = &qoderResponsesStreamToolState{
				itemID:      "item_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				outputIndex: nextOutputIndex,
			}
			nextOutputIndex++
			toolStates[index] = state
		}
		if call.ID != "" {
			state.callID = toResponsesCallIDForQoder(call.ID)
		}
		if state.callID == "" {
			state.callID = fmt.Sprintf("call_%d", index)
		}
		if call.Name != "" {
			state.name = call.Name
		}
		if call.Arguments != "" {
			state.arguments = call.Arguments
		}
		if state.added || state.name == "" {
			return state, nil
		}
		if err := closeMessage(); err != nil {
			return state, err
		}
		state.added = true
		return state, writeEvent(apicompat.ResponsesStreamEvent{
			Type:        "response.output_item.added",
			OutputIndex: state.outputIndex,
			Item: &apicompat.ResponsesOutput{
				Type:   "function_call",
				ID:     state.itemID,
				CallID: state.callID,
				Name:   state.name,
				Status: "in_progress",
			},
		})
	}
	writeToolDelta := func(event qoder.SSEEvent) error {
		if qoderToolCallDeltaIsEmptyPlaceholder(normalizeQoderOutboundToolCallEvent(event)) {
			return nil
		}
		index := toolIndexForEvent(event)
		deltas := toolAccumulator.AppendDelta(event)
		if len(deltas) == 0 {
			return nil
		}
		state, err := ensureToolAdded(index)
		if err != nil {
			return err
		}
		for _, rawDelta := range deltas {
			delta, _ := rawDelta.(map[string]any)
			function, _ := delta["function"].(map[string]any)
			arguments, _ := function["arguments"].(string)
			if arguments == "" || qoderToolArgumentDeltaIsEmptyPlaceholder(event) || !state.added {
				continue
			}
			if err := writeEvent(apicompat.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.delta",
				OutputIndex: state.outputIndex,
				ItemID:      state.itemID,
				CallID:      state.callID,
				Name:        state.name,
				Delta:       arguments,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	closeTools := func() error {
		indexes := make([]int, 0, len(toolStates))
		for index := range toolStates {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			state := toolStates[index]
			if state == nil {
				continue
			}
			if _, err := ensureToolAdded(index); err != nil {
				return err
			}
			if !state.added || state.done {
				continue
			}
			if err := writeEvent(apicompat.ResponsesStreamEvent{
				Type:        "response.function_call_arguments.done",
				OutputIndex: state.outputIndex,
				ItemID:      state.itemID,
				CallID:      state.callID,
				Name:        state.name,
				Arguments:   firstNonEmptyQoder(state.arguments, "{}"),
			}); err != nil {
				return err
			}
			item := apicompat.ResponsesOutput{
				Type:      "function_call",
				ID:        state.itemID,
				CallID:    state.callID,
				Name:      state.name,
				Arguments: firstNonEmptyQoder(state.arguments, "{}"),
				Status:    "completed",
			}
			if err := writeEvent(apicompat.ResponsesStreamEvent{
				Type:        "response.output_item.done",
				OutputIndex: state.outputIndex,
				Item:        &item,
			}); err != nil {
				return err
			}
			completedOutputs[state.outputIndex] = item
			state.done = true
		}
		return nil
	}
	finalized := false
	finish := func() error {
		if finalized {
			return nil
		}
		finalized = true
		if err := closeMessage(); err != nil {
			return err
		}
		if err := closeTools(); err != nil {
			return err
		}
		finalUsage := result.Usage
		if usageMapper != nil {
			finalUsage = usageMapper(finalUsage)
		}
		return writeEvent(apicompat.ResponsesStreamEvent{
			Type: "response.completed",
			Response: &apicompat.ResponsesResponse{
				ID:     responseID,
				Object: "response",
				Model:  model,
				Status: "completed",
				Output: completedOutputList(),
				Usage:  qoderResponsesUsage(finalUsage),
			},
		})
	}

	if err := streamQoderEvents(ctx, resp, func(event qoder.SSEEvent) error {
		if event.HasUsage {
			mergeQoderUsageEvent(&result.Usage, event)
			result.UsageDetails = event.UsageDetails
			return nil
		}
		if event.IsDone {
			return finish()
		}
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				result.HasOutput = true
				if err := openMessage(); err != nil {
					return err
				}
				_, _ = messageText.WriteString(event.Text)
				return writeEvent(apicompat.ResponsesStreamEvent{
					Type:         "response.output_text.delta",
					OutputIndex:  messageOutputIndex,
					ContentIndex: 0,
					ItemID:       messageItemID,
					Delta:        event.Text,
				})
			}
		case "tool_call_delta":
			result.HasOutput = true
			return writeToolDelta(event)
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
	if err := finish(); err != nil {
		return nil, err
	}
	return result, nil
}

type qoderResponsesStreamToolState struct {
	itemID      string
	callID      string
	name        string
	arguments   string
	outputIndex int
	added       bool
	done        bool
}

func toResponsesCallIDForQoder(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "call_") {
		return trimmed
	}
	return "call_" + trimmed
}

func qoderResponsesUsage(usage ClaudeUsage) *apicompat.ResponsesUsage {
	inputTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	out := &apicompat.ResponsesUsage{
		InputTokens:  inputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  inputTokens + usage.OutputTokens,
	}
	if usage.CacheReadInputTokens > 0 {
		out.InputTokensDetails = &apicompat.ResponsesInputTokensDetails{CachedTokens: usage.CacheReadInputTokens}
	}
	return out
}

type qoderAnthropicContentWriter struct {
	w                 io.Writer
	nextIndex         int
	openTextIndex     *int
	openThinkingIndex *int
	pendingToolCalls  *qoderOpenAIToolCallAccumulator
	mapToolName       qoderToolNameMapper
	sawToolCall       bool
}

func newQoderAnthropicContentWriter(w io.Writer, toolNameMapper ...qoderToolNameMapper) *qoderAnthropicContentWriter {
	writer := &qoderAnthropicContentWriter{w: w}
	if len(toolNameMapper) > 0 {
		writer.mapToolName = toolNameMapper[0]
	}
	return writer
}

func (w *qoderAnthropicContentWriter) writeTextDelta(text string) error {
	if strings.TrimSpace(text) == "" && text == "" {
		return nil
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
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

func (w *qoderAnthropicContentWriter) writeThinkingDelta(text string) error {
	if text == "" {
		return nil
	}
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.flushPendingToolCalls(); err != nil {
		return err
	}
	if w.openThinkingIndex == nil {
		index := w.nextIndex
		w.nextIndex++
		if err := writeAnthropicSSE(w.w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         index,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		}); err != nil {
			return err
		}
		w.openThinkingIndex = &index
	}
	return writeAnthropicSSE(w.w, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": *w.openThinkingIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text},
	})
}

func (w *qoderAnthropicContentWriter) writeToolCall(event qoder.SSEEvent) error {
	normalized := normalizeQoderOutboundToolCallEvent(event)
	if qoderToolCallDeltaIsEmptyPlaceholder(normalized) {
		return nil
	}
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
		return err
	}
	if w.pendingToolCalls == nil {
		w.pendingToolCalls = newQoderOpenAIToolCallAccumulator(w.mapToolName)
	}
	if len(w.pendingToolCalls.AppendDelta(normalized)) > 0 {
		w.sawToolCall = true
	}
	return nil
}

func (w *qoderAnthropicContentWriter) closeOpenBlock() error {
	if err := w.closeOpenTextBlock(); err != nil {
		return err
	}
	if err := w.closeOpenThinkingBlock(); err != nil {
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

func (w *qoderAnthropicContentWriter) closeOpenThinkingBlock() error {
	if w.openThinkingIndex == nil {
		return nil
	}
	index := *w.openThinkingIndex
	w.openThinkingIndex = nil
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
			if _, err := qoderAnthropicToolInput(arguments); err != nil {
				return err
			}
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

func (w *qoderAnthropicContentWriter) ensureContentBlock() error {
	if w == nil || w.nextIndex > 0 {
		return nil
	}
	index := w.nextIndex
	w.nextIndex++
	if err := writeAnthropicSSE(w.w, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         index,
		"content_block": map[string]any{"type": "text", "text": ""},
	}); err != nil {
		return err
	}
	return writeAnthropicSSE(w.w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
}

func (w *qoderAnthropicContentWriter) stopReason() string {
	if w != nil && w.sawToolCall {
		return "tool_use"
	}
	return "end_turn"
}

type qoderEventResult struct {
	events []qoder.SSEEvent
	err    error
}

func qoderSendEventResult(ctx context.Context, results chan<- qoderEventResult, result qoderEventResult) bool {
	select {
	case results <- result:
		return true
	case <-ctx.Done():
		return false
	}
}

func scanQoderEvents(ctx context.Context, resp *http.Response, results chan<- qoderEventResult) {
	defer close(results)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)
	for scanner.Scan() {
		parsed, err := qoder.ParseSSELine(scanner.Text())
		if err != nil {
			qoderSendEventResult(ctx, results, qoderEventResult{err: err})
			return
		}
		if len(parsed) > 0 && !qoderSendEventResult(ctx, results, qoderEventResult{events: parsed}) {
			return
		}
		for _, event := range parsed {
			if event.IsDone {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		qoderSendEventResult(ctx, results, qoderEventResult{err: err})
	}
}

func streamQoderEvents(ctx context.Context, resp *http.Response, handle func(qoder.SSEEvent) error, keepalive func() error) error {
	if resp == nil || resp.Body == nil {
		return errors.New("qoder response body is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer func() { _ = resp.Body.Close() }()
	defer cancel()

	ticker := time.NewTicker(qoderKeepaliveEvery)
	defer ticker.Stop()
	results := make(chan qoderEventResult, 1)
	transformer := newQoderTextToolCallTransformer()
	go scanQoderEvents(ctx, resp, results)
	for {
		select {
		case result, ok := <-results:
			if !ok {
				for _, event := range transformer.Flush() {
					if err := handle(event); err != nil {
						return err
					}
				}
				return nil
			}
			if result.err != nil {
				return result.err
			}
			for _, event := range result.events {
				for _, transformed := range transformer.Append(event) {
					if err := handle(transformed); err != nil {
						return err
					}
					if transformed.IsDone {
						return nil
					}
				}
			}
		case <-ctx.Done():
			return ctx.Err()
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

func BuildQoderOpenAICompletion(model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	events = normalizeQoderTextToolCallEvents(events)
	content := qoderTextFromEvents(events)
	usage := qoderUsageFromEvents(events)
	totalTokens := qoderTotalTokensFromEvents(events, usage)
	usageDetails := qoderUsageDetailsFromEvents(events)
	toolCalls := qoderOpenAIToolCallsFromEvents(events, toolNameMappers...)
	message := map[string]any{"role": "assistant", "content": content}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		if content == "" {
			message["content"] = nil
		}
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
		"usage": qoderOpenAIUsage(usage, totalTokens, usageDetails),
	})
}

func BuildQoderAnthropicMessage(model string, events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]byte, error) {
	events = normalizeQoderTextToolCallEvents(events)
	contentBlocks, err := qoderAnthropicContentBlocksFromEvents(events, toolNameMappers...)
	if err != nil {
		return nil, err
	}
	usage := qoderUsageFromEvents(events)
	usageDetails := qoderUsageDetailsFromEvents(events)
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
		"usage":         qoderAnthropicUsage(usage, usageDetails),
	})
}

func qoderOpenAIToolCallsFromEvents(events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) []any {
	acc := newQoderOpenAIToolCallAccumulator(toolNameMappers...)
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.Calls()
}

func qoderEventsHaveToolCalls(events []qoder.SSEEvent) bool {
	acc := newQoderOpenAIToolCallAccumulator()
	for _, event := range events {
		if event.Type == "tool_call_delta" {
			acc.AppendDelta(event)
		}
	}
	return acc.HasToolCalls()
}

func qoderAnthropicContentBlocksFromEvents(events []qoder.SSEEvent, toolNameMappers ...qoderToolNameMapper) ([]any, error) {
	blocks := make([]any, 0)
	var text bytes.Buffer
	var thinking bytes.Buffer
	var toolAccumulator *qoderOpenAIToolCallAccumulator
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": text.String()})
		text.Reset()
	}
	flushThinking := func() {
		if thinking.Len() == 0 {
			return
		}
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": thinking.String()})
		thinking.Reset()
	}
	flushTools := func() error {
		if toolAccumulator == nil {
			return nil
		}
		for _, rawCall := range toolAccumulator.Calls() {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			function, _ := call["function"].(map[string]any)
			input, err := qoderAnthropicToolInput(function["arguments"])
			if err != nil {
				return err
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    call["id"],
				"name":  function["name"],
				"input": input,
			})
		}
		toolAccumulator = nil
		return nil
	}
	for _, event := range events {
		switch event.Type {
		case "text_delta":
			if event.Text != "" {
				if err := flushTools(); err != nil {
					return nil, err
				}
				flushThinking()
				_, _ = text.WriteString(event.Text)
			}
		case "reasoning_delta":
			if event.Text != "" {
				flushText()
				if err := flushTools(); err != nil {
					return nil, err
				}
				_, _ = thinking.WriteString(event.Text)
			}
		case "tool_call_delta":
			if qoderToolCallDeltaIsEmptyPlaceholder(normalizeQoderOutboundToolCallEvent(event)) {
				continue
			}
			if toolAccumulator == nil {
				toolAccumulator = newQoderOpenAIToolCallAccumulator(toolNameMappers...)
			}
			if len(toolAccumulator.AppendDelta(event)) > 0 {
				flushText()
				flushThinking()
			}
		}
	}
	flushText()
	flushThinking()
	if err := flushTools(); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return []any{map[string]any{"type": "text", "text": ""}}, nil
	}
	return blocks, nil
}

func qoderAnthropicToolInput(raw any) (map[string]any, error) {
	args := qoderToolArgumentsString(raw)
	if strings.TrimSpace(args) == "" {
		return map[string]any{}, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("malformed qoder tool arguments: %s", args)
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

func qoderUsageDetailsFromEvents(events []qoder.SSEEvent) qoder.UsageDetails {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].HasUsage {
			return events[i].UsageDetails
		}
	}
	return qoder.UsageDetails{}
}

func mergeQoderUsageEvent(usage *ClaudeUsage, event qoder.SSEEvent) {
	if usage == nil || !event.HasUsage {
		return
	}
	cachedTokens := 0
	if event.UsageDetails.PromptTokensDetails != nil {
		cachedTokens = event.UsageDetails.PromptTokensDetails.CachedTokens
		usage.InputTokens = max(event.PromptTokens-cachedTokens, 0)
		usage.CacheReadInputTokens = cachedTokens
		usage.CacheCreationInputTokens = 0
	} else {
		usage.InputTokens = event.PromptTokens
		usage.CacheReadInputTokens = 0
	}
	usage.OutputTokens = event.CompletionTokens
}

func qoderEventsWithUsage(events []qoder.SSEEvent, usage ClaudeUsage) []qoder.SSEEvent {
	if len(events) == 0 {
		return events
	}
	out := append([]qoder.SSEEvent(nil), events...)
	for i := range out {
		if !out[i].HasUsage {
			continue
		}
		if out[i].PromptTokens == 0 {
			out[i].PromptTokens = usage.InputTokens + usage.CacheReadInputTokens
		}
		if out[i].CompletionTokens == 0 {
			out[i].CompletionTokens = usage.OutputTokens
		}
		if out[i].TotalTokens == 0 {
			out[i].TotalTokens = out[i].PromptTokens + out[i].CompletionTokens
		}
	}
	return out
}

func qoderTotalTokensFromEvents(events []qoder.SSEEvent, usage ClaudeUsage) int {
	var lastPromptTokens int
	var lastCompletionTokens int
	for i := len(events) - 1; i >= 0; i-- {
		if !events[i].HasUsage {
			continue
		}
		if events[i].TotalTokens > 0 {
			return events[i].TotalTokens
		}
		lastPromptTokens = events[i].PromptTokens
		lastCompletionTokens = events[i].CompletionTokens
		break
	}
	if lastPromptTokens > 0 || lastCompletionTokens > 0 {
		return lastPromptTokens + lastCompletionTokens
	}
	return usage.InputTokens + usage.OutputTokens
}

func qoderOpenAIUsage(usage ClaudeUsage, totalTokens int, usageDetails ...qoder.UsageDetails) map[string]any {
	promptTokens := usage.InputTokens + usage.CacheReadInputTokens
	completionTokens := usage.OutputTokens
	if totalTokens > 0 && totalTokens >= completionTokens {
		promptTokens = totalTokens - completionTokens
	}
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	out := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
	details := qoderFirstUsageDetails(usageDetails)
	if details.PromptTokensDetails != nil {
		out["prompt_tokens_details"] = map[string]any{
			"cached_tokens":    details.PromptTokensDetails.CachedTokens,
			"cacheable_tokens": details.PromptTokensDetails.CacheableTokens,
		}
	} else if usage.CacheReadInputTokens > 0 {
		out["prompt_tokens_details"] = map[string]any{"cached_tokens": usage.CacheReadInputTokens}
	}
	if details.CompletionTokensDetails != nil {
		out["completion_tokens_details"] = map[string]any{"reasoning_tokens": details.CompletionTokensDetails.ReasoningTokens}
	}
	return out
}

func qoderAnthropicUsage(usage ClaudeUsage, usageDetails ...qoder.UsageDetails) map[string]any {
	out := map[string]any{
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	}
	details := qoderFirstUsageDetails(usageDetails)
	if usage.CacheReadInputTokens > 0 || details.PromptTokensDetails != nil {
		out["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		out["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	}
	return out
}

func qoderFirstUsageDetails(details []qoder.UsageDetails) qoder.UsageDetails {
	if len(details) == 0 {
		return qoder.UsageDetails{}
	}
	return details[0]
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

func openAIUsageChunk(id, model string, usage ClaudeUsage, totalTokens int, usageDetails ...qoder.UsageDetails) map[string]any {
	chunk := openAIChunk(id, model, map[string]any{}, nil)
	chunk["usage"] = qoderOpenAIUsage(usage, totalTokens, usageDetails...)
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

func latestQoderPromptText(messages []qoderMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if text := qoderPromptTextForMessage(messages[i]); text != "" {
			return text
		}
	}
	return ""
}

func qoderPromptTextForMessage(message qoderMessage) string {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return ""
	}
	if message.Role != "tool" {
		return message.Text
	}
	toolCallID := qoderMessageToolCallID(message)
	if toolCallID == "" {
		return message.Text
	}
	return fmt.Sprintf("<tool_result id=\"%s\">\n%s\n</tool_result>", html.EscapeString(toolCallID), message.Text)
}

func latestQoderPayloadPromptText(messages []qoderMessage, includeTools bool) string {
	if text := qoderToolContinuationPromptText(messages); text != "" {
		return text
	}
	if text := latestQoderUserText(messages); text != "" {
		return text
	}
	return latestQoderPromptText(messages)
}

func qoderToolContinuationPromptText(messages []qoderMessage) string {
	toolStart := len(messages)
	for toolStart > 0 && messages[toolStart-1].Role == "tool" {
		toolStart--
	}
	if toolStart == len(messages) {
		return ""
	}

	// Tool-only Responses continuations are valid when previous_response_id carries
	// the prior assistant tool call. Otherwise require the current replay to include
	// an immediately preceding assistant tool-call turn, so ordinary historical tool
	// messages are not promoted to the active prompt by accident.
	if toolStart > 0 && !qoderMessageHasToolCalls(messages[toolStart-1]) {
		return ""
	}

	parts := make([]string, 0, len(messages)-toolStart)
	for _, message := range messages[toolStart:] {
		if text := qoderPromptTextForMessage(message); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
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

func qoderTextFromEvents(events []qoder.SSEEvent) string {
	var buf bytes.Buffer
	for _, event := range events {
		if event.Type == "text_delta" && event.Text != "" {
			_, _ = buf.WriteString(event.Text)
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
