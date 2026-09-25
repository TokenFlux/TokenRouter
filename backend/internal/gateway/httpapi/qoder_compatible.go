// Qoder Messages/Responses 的 HTTP 边界保留原校验与等待顺序。
package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// QoderEndpoint 保留三种 HTTP 外形，不作为任意供应商识别规则。
type QoderEndpoint string

const (
	QoderChat      QoderEndpoint = "chat_completions"
	QoderMessages  QoderEndpoint = "messages"
	QoderResponses QoderEndpoint = "responses"
)

type QoderCompatibleCall struct {
	Endpoint           QoderEndpoint
	Key                *apikey.APIKey
	Subject            authctx.AuthSubject
	Subscription       *billing.UserSubscription
	Body, AttemptBody  []byte
	Model, SessionHash string
	Stream             bool
	StreamStarted      *bool
	Plan               routing.RoutePlan
	Log                *zap.Logger
}
type QoderCompatibleBackend interface {
	Enter() (func(), error)
	Access(*gin.Context) (*apikey.APIKey, bool)
	BindErrors(*gin.Context)
	PrepareClient(*gin.Context, []byte, QoderEndpoint)
	ObserveRequest(*gin.Context, string, bool)
	ObserveEndpoint(*gin.Context, bool)
	ObserveAuthLatency(*gin.Context, time.Duration)
	Plan(*gin.Context, *apikey.APIKey, string) routing.RoutePlan
	AttemptBody([]byte, routing.RoutePlan) []byte
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	SessionHash(*gin.Context, QoderEndpoint, []byte, int64) string
	Execution(*gin.Context, QoderCompatibleCall) textflow.QoderCompatiblePorts
	ConcurrencyError(*gin.Context, error, string, bool, QoderEndpoint)
}
type QoderCompatibleHandler struct {
	requestLifetime

	backend     QoderCompatibleBackend
	concurrency *ConcurrencyHelper
	maxAccounts int
}

func NewQoderCompatibleHandler(backend QoderCompatibleBackend, concurrency *ConcurrencyHelper, max int) *QoderCompatibleHandler {
	return &QoderCompatibleHandler{backend: backend, concurrency: concurrency, maxAccounts: max}
}
func (h *QoderCompatibleHandler) Messages(c *gin.Context)  { h.handle(c, QoderMessages) }
func (h *QoderCompatibleHandler) Responses(c *gin.Context) { h.handle(c, QoderResponses) }

// ChatCompletions 提供手写装配入口；生产 Chat 使用 Execute 契约。
func (h *QoderCompatibleHandler) ChatCompletions(c *gin.Context) { h.handle(c, QoderChat) }
func (h *QoderCompatibleHandler) errorResponse(c *gin.Context, status int, kind, message string, endpoint QoderEndpoint) {
	WriteQoderError(c, status, kind, message, endpoint)
}
func (h *QoderCompatibleHandler) handleConcurrencyError(c *gin.Context, err error, kind string, started bool, endpoint QoderEndpoint) {
	h.backend.ConcurrencyError(c, err, kind, started, endpoint)
}
func qoderReleaseMode(stream bool) scheduler.ReleaseMode {
	if stream {
		return scheduler.ReleaseOnCompletion
	}
	return scheduler.ReleaseOnCancel
}
func (h *QoderCompatibleHandler) handle(c *gin.Context, endpoint QoderEndpoint) {
	format := "openai"
	if endpoint == QoderMessages {
		format = "anthropic"
	}
	done, accepted := h.beginRequest(c, format)
	if !accepted {
		return
	}
	defer done()

	{
		done, err := h.backend.Enter()
		if err != nil {
			h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Service is shutting down", endpoint)
			return
		}
		defer done()
	}

	streamStarted := false
	requestStart := time.Now()

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key", endpoint)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found", endpoint)
		return
	}
	h.backend.BindErrors(c)
	reqLog := RequestLogger(
		c,
		"handler.qoder_gateway."+string(endpoint),
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	body, err := ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := countMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit), endpoint)
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body", endpoint)
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty", endpoint)
		return
	}
	if !gjson.ValidBytes(body) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body", endpoint)
		return
	}
	h.backend.PrepareClient(c, body, endpoint)
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || strings.TrimSpace(modelResult.String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required", endpoint)
		return
	}
	reqModel := strings.TrimSpace(modelResult.String())
	reqStream, ok := ParseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", InvalidStreamFieldTypeMessage, endpoint)
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))
	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)
	plan := h.backend.Plan(c, apiKey, reqModel)
	forwardBody := h.backend.AttemptBody(body, plan)

	subscription, _ := SubscriptionFromContext(c)
	h.backend.ObserveAuthLatency(c, time.Since(requestStart))

	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("qoder.billing_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message, endpoint)
		return
	}
	sessionHash := h.backend.SessionHash(c, endpoint, body, apiKey.ID)

	maxWait := scheduler.CalculateMaxWait(subject.Concurrency)
	waitCounted := false
	if h.concurrency != nil {
		waitEntry, err := h.concurrency.EnterUserWait(c.Request.Context(), subject.UserID, maxWait)
		canWait := waitEntry.Allowed
		if err != nil {
			reqLog.Warn("qoder.user_wait_counter_increment_failed", zap.Error(err))
		} else if !canWait {
			h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later", endpoint)
			return
		} else {
			waitCounted = true
		}
		defer func() {
			if waitCounted {
				waitEntry.Release()
			}
		}()

		userRelease, err := h.concurrency.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
		if err != nil {
			reqLog.Warn("qoder.user_slot_acquire_failed", zap.Error(err))
			h.handleConcurrencyError(c, err, "user", streamStarted, endpoint)
			return
		}
		if waitCounted {
			waitEntry.Release()
			waitCounted = false
		}
		userRelease = scheduler.WrapRelease(c.Request.Context(), qoderReleaseMode(reqStream), userRelease)
		if userRelease != nil {
			defer userRelease()
		}
	}

	call := QoderCompatibleCall{Endpoint: endpoint, Key: apiKey, Subject: subject, Subscription: subscription, Body: body, AttemptBody: forwardBody, Model: reqModel, Stream: reqStream, StreamStarted: &streamStarted, SessionHash: sessionHash, Plan: plan, Log: reqLog}
	textflow.RunQoderCompatible(h.backend.Execution(c, call), h.maxAccounts)
}
