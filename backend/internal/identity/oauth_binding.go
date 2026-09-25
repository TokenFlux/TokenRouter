// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// OAuthBindingSigner 保留现有用户 ID 与 HMAC cookie 格式，HTTP 不读取完整配置。
type OAuthBindingSigner struct{ secret string }

func NewOAuthBindingSigner(secret string) OAuthBindingSigner {
	return OAuthBindingSigner{secret: secret}
}
func (s OAuthBindingSigner) Sign(id int64) (string, error) {
	return BuildOAuthBindUserCookieValue(id, s.secret)
}
func (s OAuthBindingSigner) Verify(value string) (int64, error) {
	return ParseOAuthBindUserCookieValue(value, s.secret)
}
func BuildOAuthBindUserCookieValue(userID int64, secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if userID <= 0 || secret == "" {
		return "", errors.New("invalid oauth bind cookie input")
	}
	payload := strconv.FormatInt(userID, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}
func ParseOAuthBindUserCookieValue(value string, secret string) (int64, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return 0, errors.New("missing oauth bind cookie secret")
	}
	payload, signature, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok || payload == "" || signature == "" {
		return 0, errors.New("invalid oauth bind cookie")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
		return 0, errors.New("invalid oauth bind cookie signature")
	}
	userID, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || userID <= 0 {
		return 0, errors.New("invalid oauth bind cookie user")
	}
	return userID, nil
}
