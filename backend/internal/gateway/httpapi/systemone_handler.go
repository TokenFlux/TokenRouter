package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/systemone"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
)

// SystemOneHTTPExecution 输出当前请求的最终错误。
type SystemOneHTTPExecution interface {
	systemone.Ports
	End(error)
}

// SystemOneHTTPPorts 绑定 SystemOne 的请求工厂和内容审核。
type SystemOneHTTPPorts interface {
	NewSystemOne(*gin.Context, AuxiliaryHTTPInput, *zap.Logger, *bool) SystemOneHTTPExecution
	ModerateSystemOne(*gin.Context, *zap.Logger, string, []byte) bool
}

// SystemOne 处理同步决策请求，提供商切换由 systemone.Run 执行。
func (h *AuxiliaryHandler) SystemOne(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()
	started := time.Now()
	stream := false
	access, ok := h.ports.Access(c)
	if !ok {
		h.ports.Error(c, 401, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := h.ports.Subject(c)
	if !ok {
		h.ports.Error(c, 500, "api_error", "User context not found")
		return
	}
	log := h.ports.Logger(c, "handler.systemone", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", access.ID))
	ports, ok := h.ports.(SystemOneHTTPPorts)
	if !ok || !h.ports.Dependencies(c, log) {
		h.ports.Error(c, 503, "api_error", "SystemOne is not configured")
		return
	}
	h.ports.BindErrors(c)
	body, ok := h.base.readBody(c)
	if !ok {
		return
	}
	request, err := wire.ParseRequest(body)
	if err != nil {
		h.ports.Error(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	h.ports.ObserveRequest(c, request.Model, false, true)
	_, mapping := h.ports.Plan(c, request.Model, true)
	if ports.ModerateSystemOne(c, log, request.Model, request.ModerationBody()) {
		return
	}
	h.ports.AuthLatency(c, time.Since(started))
	release, ok := h.ports.AcquireUser(c, subject, false, &stream, log)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}
	if failure := h.ports.Billing(c); failure != nil {
		if failure.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(failure.RetryAfter))
		}
		h.ports.Error(c, failure.Status, failure.Code, failure.Message)
		return
	}
	run := ports.NewSystemOne(c, AuxiliaryHTTPInput{Subject: subject, Model: request.Model, Body: body, Mapping: mapping}, log, &stream)
	run.End(systemone.Run(c.Request.Context(), h.ports.MaxSwitches(), run))
}
