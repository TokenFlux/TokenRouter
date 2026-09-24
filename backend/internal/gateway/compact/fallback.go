package compact

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

// Failure 携带尚未提交的压缩失败，HTTP 与恢复用例使用同一错误值。
type Failure struct {
	Payload []byte
	Message string
}

func (f *Failure) Error() string {
	if f == nil || strings.TrimSpace(f.Message) == "" {
		return "upstream compact request failed"
	}
	return f.Message
}

// Models 按需读取账号专用规则和全局回退，不提前触发无须执行的映射。
type Models interface {
	AccountModel(string) (string, bool)
	GlobalModel() string
	ResolveGlobalModel(string) string
}

// Recovery 只持有模型投影和纯协议操作；不拥有账号切换或第二套重试循环。
type Recovery struct {
	Models        Models
	ContextWindow func(string, []byte) bool
	RewriteModel  func([]byte, string) []byte
}

// Request 固化当前请求的压缩意图，保持路径/触发器/native-v2 状态不变。
type Request struct {
	Explicit, AlreadyRetried bool
	RequestedModel           string
	Body                     []byte
}

// ResolveModel 保留账号 compact 专用规则优先，以及全局规则的延迟求值。
func (r Recovery) ResolveModel(requested string) string {
	requested = strings.TrimSpace(requested)
	if mapped, ok := r.Models.AccountModel(requested); ok {
		if mapped = strings.TrimSpace(mapped); mapped != "" {
			return mapped
		}
	}
	fallback := strings.TrimSpace(r.Models.GlobalModel())
	if fallback == "" {
		return ""
	}
	return strings.TrimSpace(r.Models.ResolveGlobalModel(fallback))
}

// NewFailure 只为显式压缩请求创建恢复信号，不改变普通失败响应。
func (r Recovery) NewFailure(explicit bool, payload []byte, message string) *Failure {
	if !explicit || !r.ModelFailure(400, message, payload) {
		return nil
	}
	return &Failure{Payload: append([]byte(nil), payload...), Message: logredact.SanitizeUpstreamQueries(strings.TrimSpace(message))}
}

// Prepare 仅允许一次同账号恢复，调用方仍独占输出与重试窗口。
func (r Recovery) Prepare(in Request, status int, message string, payload []byte) ([]byte, string, bool) {
	if in.AlreadyRetried || !in.Explicit || !r.ModelFailure(status, message, payload) {
		return in.Body, "", false
	}
	fallback := r.ResolveModel(in.RequestedModel)
	current := strings.TrimSpace(gjson.GetBytes(in.Body, "model").String())
	if fallback == "" || strings.EqualFold(fallback, current) {
		return in.Body, "", false
	}
	body := r.RewriteModel(in.Body, fallback)
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "model").String()), current) {
		return in.Body, "", false
	}
	return body, fallback, true
}

// RetryEffects 是恢复决定后的单步输出/资源操作，核心规定顺序。
type RetryEffects interface {
	ObserveRetry([]byte, string)
	CloseResponse()
	SetModel(string)
	LogRetry(string, string, string)
}

// ApplySignal 保留先记录恢复、关闭旧响应、更新模型观测、记录诊断的顺序。
func (r Recovery) ApplySignal(in Request, failure *Failure, p RetryEffects) ([]byte, string, bool) {
	if failure == nil {
		return in.Body, "", false
	}
	body, model, retry := r.Prepare(in, 400, failure.Message, failure.Payload)
	if !retry {
		return in.Body, "", false
	}
	p.ObserveRetry(failure.Payload, failure.Message)
	p.CloseResponse()
	from := strings.TrimSpace(gjson.GetBytes(in.Body, "model").String())
	p.SetModel(model)
	p.LogRetry(from, model, upstream.ExtractErrorCode(failure.Payload))
	return body, model, true
}

// ModelFailure 复用平台提供的上下文窗口分类，拥有网关压缩恢复资格。
func (r Recovery) ModelFailure(statusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if r.ContextWindow(upstreamMsg, upstreamBody) {
		return true
	}
	if statusCode != 400 && statusCode != 404 {
		return false
	}

	values := []string{
		upstream.ExtractErrorCode(upstreamBody),
		upstreamMsg,
		gjson.GetBytes(upstreamBody, "error.type").String(),
		gjson.GetBytes(upstreamBody, "response.error.code").String(),
		gjson.GetBytes(upstreamBody, "response.error.type").String(),
	}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "model_not_found", "model_not_available", "unsupported_model", "invalid_model":
			return true
		}
		if ExplicitModelAvailabilityMessage(value) {
			return true
		}
	}
	// 仅失败空壳允许重试；业务或策略错误保留原报文。
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(upstreamBody, "response.status").String()), "failed") ||
		strings.EqualFold(strings.TrimSpace(gjson.GetBytes(upstreamBody, "status").String()), "failed") {
		for _, path := range []string{
			"error.message", "error.code", "error.type",
			"response.error.message", "response.error.code", "response.error.type",
		} {
			if strings.TrimSpace(gjson.GetBytes(upstreamBody, path).String()) != "" {
				return false
			}
		}
		return strings.TrimSpace(upstreamMsg) == ""
	}
	return false
}

func ExplicitModelAvailabilityMessage(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, phrase := range []string{
		"model not found",
		"model does not exist",
		"model is unavailable",
		"model is not available",
		"model is unsupported",
		"model is not supported",
		"unsupported model",
	} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	// 只接受以模型为主体的明确不可用消息，避免误判不支持的模型输出特性。
	if strings.HasPrefix(value, "the model ") || strings.HasPrefix(value, "model ") {
		return strings.Contains(value, " does not exist") ||
			strings.Contains(value, " was not found") ||
			strings.Contains(value, " is unavailable") ||
			strings.Contains(value, " is not available")
	}
	return false
}

func NormalizeHTTPErrorPayload(signal *Failure) []byte {
	if signal == nil {
		return nil
	}
	payload := append([]byte(nil), signal.Payload...)
	var terminal struct {
		Error    json.RawMessage `json:"error"`
		Response struct {
			Error json.RawMessage `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &terminal) != nil || len(bytes.TrimSpace(terminal.Response.Error)) == 0 ||
		bytes.Equal(bytes.TrimSpace(terminal.Response.Error), []byte("null")) {
		return payload
	}
	// 只在流转 HTTP 边界把嵌套 error 提到原 HTTP 错误 envelope。
	normalized, err := json.Marshal(struct {
		Error json.RawMessage `json:"error"`
	}{Error: terminal.Response.Error})
	if err != nil {
		return payload
	}
	return normalized
}

// AsFailure 识别恢复信号，保留包装错误链和非空指针约束。
func AsFailure(err error) (*Failure, bool) {
	var failure *Failure
	ok := errors.As(err, &failure)
	return failure, ok && failure != nil
}
