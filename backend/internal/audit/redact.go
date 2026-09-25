package audit

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// auditNormalizeBodyKey 归一化键名：小写并去除分隔符，
// 使 private_key / privateKey / privatekey / api-v3-key 等写法共享同一判定，
// 避免子串清单假设 snake_case 而漏掉支付渠道等无分隔符风格的密钥字段。
func auditNormalizeBodyKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		switch r {
		case '_', '-', '.', ' ':
			continue
		default:
			_, _ = b.WriteRune(r)
		}
	}
	return b.String()
}

// auditBodySensitiveExactKeys 请求体脱敏的精确匹配键（归一化后）。
// 除内置清单外，程序化并入两份权威敏感表以防清单漂移：
//   - SensitiveCredentialKeys：账号 credentials 的敏感子键（session_key / service_account_json 等）
//   - providerSensitiveConfigFields：支付渠道密钥字段（pkey / privatekey / apiv3key 等）
//
// Redactor 持有调用方投影的敏感键集合；发布后只读。
type Redactor struct{ exact map[string]struct{} }

func NewRedactor(extraKeys []string) *Redactor {
	builtin := []string{"code", "codes", "pin", "cvv", "authorization", "cookie", "x-api-key", "credential", "key", "proxy_key", "custom_key", "session"}
	set := make(map[string]struct{}, len(builtin)+len(extraKeys))
	for _, k := range append(builtin, extraKeys...) {
		set[auditNormalizeBodyKey(k)] = struct{}{}
	}
	return &Redactor{exact: set}
}

// auditBodySensitiveSubstrings 请求体脱敏的包含匹配子串（对归一化后的键名比对）。
// 命中任一子串即整体擦除该键的值（例如 new_password / secret_access_key / temp_token）。
var auditBodySensitiveSubstrings = []string{
	"password", "passwd", "secret", "token",
	"apikey", "accesskey", "privatekey",
	"otp", "credentialvalue",
	"sessionkey", "serviceaccount",
}

func (r *Redactor) IsSensitiveKey(key string) bool {
	k := auditNormalizeBodyKey(key)
	if _, ok := r.exact[k]; ok {
		return true
	}
	for _, sub := range auditBodySensitiveSubstrings {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

const auditRedactedPlaceholder = "***"

// RedactAuditBody 对请求体做审计入库前的脱敏：
//   - JSON：递归擦除敏感键的值（保留结构，base_url 等非敏感字段可见以便追责）
//   - 非 JSON：返回占位说明
//   - 超长：截断并附截断标记
func (r *Redactor) RedactBody(raw []byte, contentType string) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > AuditRequestBodyCaptureLimit {
		// raw 可能已被中间件按上限截断，实际请求体只会更大，不报具体字节数。
		return "<body omitted: exceeds " + strconv.Itoa(AuditRequestBodyCaptureLimit) + " bytes>"
	}
	ct := strings.ToLower(contentType)
	if !strings.Contains(ct, "json") || !json.Valid(raw) {
		// 表单等非 JSON 内容走文本兜底脱敏后仍可能含敏感信息，直接不入库。
		return "<non-json body omitted: " + strconv.Itoa(len(raw)) + " bytes, content-type=" + strings.TrimSpace(contentType) + ">"
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "<unparsable body omitted>"
	}
	redacted := r.redactValue(value, 0)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "<redacted>"
	}
	out := string(encoded)
	if len(out) > auditRequestBodyMaxBytes {
		out = out[:auditRequestBodyMaxBytes] + "...<truncated>"
	}
	return out
}

const auditRedactMaxDepth = 24

func (r *Redactor) redactValue(value any, depth int) any {
	if depth > auditRedactMaxDepth {
		return "<depth limit exceeded>"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			if r.IsSensitiveKey(k) {
				out[k] = auditRedactedPlaceholder
				continue
			}
			out[k] = r.redactValue(item, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = r.redactValue(item, depth+1)
		}
		return out
	default:
		return value
	}
}

func MaskAuditCredential(credential string) string { return logredact.MaskCredential(credential) }

// RedactAuditQuery 对 URL query 做轻量脱敏后返回。
func RedactAuditQuery(rawQuery string) string {
	rawQuery = strings.TrimSpace(rawQuery)
	if rawQuery == "" {
		return ""
	}
	return logredact.RedactText(rawQuery, "api_key", "apikey", "token", "secret", "key")
}
