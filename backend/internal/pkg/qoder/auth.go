package qoder

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CenterBaseURL 是 Qoder center 服务的默认地址。
const CenterBaseURL = "https://center.qoder.sh"

// APIBaseURL 是 Qoder API 的默认地址，已按 qodercli endpoint-cache.json 校验。
const APIBaseURL = "https://api1.qoder.sh"

// ClientVersion 是 COSY 协议版本，与 qodercli v1.0.20 保持一致。
const ClientVersion = "1.0.20"

// GenerateRequestID 生成随机请求 ID。
func GenerateRequestID() string {
	return hex.EncodeToString(mustRandomBytes(16))
}

// RandomToken 生成指定长度的 URL-safe 随机 token。
func RandomToken(length int) string {
	return base64URLEncode(mustRandomBytes(length))
}

// RandomHex 生成指定长度的十六进制随机字符串。
func RandomHex(length int) string {
	b := mustRandomBytes((length + 1) / 2)
	return hex.EncodeToString(b)[:length]
}

func mustRandomBytes(length int) []byte {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("qoder random bytes: %w", err))
	}
	return b
}

func base64URLEncode(b []byte) string {
	// 手动实现不带 padding 的 URL-safe base64 编码。
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var result strings.Builder
	result.Grow((len(b)*8 + 5) / 6)

	for i := 0; i < len(b); i += 3 {
		var val uint32
		remaining := len(b) - i

		val |= uint32(b[i]) << 16
		if remaining > 1 {
			val |= uint32(b[i+1]) << 8
		}
		if remaining > 2 {
			val |= uint32(b[i+2])
		}

		_ = result.WriteByte(alphabet[(val>>18)&0x3f])
		_ = result.WriteByte(alphabet[(val>>12)&0x3f])
		if remaining > 1 {
			_ = result.WriteByte(alphabet[(val>>6)&0x3f])
		}
		if remaining > 2 {
			_ = result.WriteByte(alphabet[val&0x3f])
		}
	}
	return result.String()
}

// NewMachine 创建新的随机机器身份。
func NewMachine() *MachineIdentity {
	return &MachineIdentity{
		MachineID:    RandomHex(36), // UUID 格式长度
		MachineToken: RandomToken(50),
		MachineType:  RandomHex(18),
	}
}

// ExchangePAT 使用 Personal Access Token 换取 AuthIdentity。
func ExchangePAT(pat string, machine *MachineIdentity, centerURL string) (*AuthIdentity, error) {
	return ExchangePATContext(context.Background(), pat, machine, centerURL, nil)
}

// ExchangePATContext 使用传入的 context 和请求执行器换取 AuthIdentity。
func ExchangePATContext(ctx context.Context, pat string, machine *MachineIdentity, centerURL string, doer RequestDoer) (*AuthIdentity, error) {
	inner := map[string]any{
		"personalToken":      pat,
		"securityOauthToken": "",
		"refreshToken":       "",
		"needRefresh":        false,
		"authInfo":           map[string]any{},
	}
	return exchangeJobToken(ctx, inner, machine, centerURL, "PAT exchange", doer)
}

// RefreshSession 使用 Qoder refresh_token 换取新的 COSY 身份。
func RefreshSession(refreshToken, securityOauthToken string, machine *MachineIdentity, centerURL string) (*AuthIdentity, error) {
	return RefreshSessionContext(context.Background(), refreshToken, securityOauthToken, machine, centerURL, nil)
}

// RefreshSessionContext 使用传入的 context 和请求执行器刷新 COSY 身份。
func RefreshSessionContext(ctx context.Context, refreshToken, securityOauthToken string, machine *MachineIdentity, centerURL string, doer RequestDoer) (*AuthIdentity, error) {
	if strings.TrimSpace(securityOauthToken) == "" {
		return nil, fmt.Errorf("qoder: refresh requires securityOauthToken")
	}
	inner := map[string]any{
		"personalToken":      "",
		"securityOauthToken": strings.TrimSpace(securityOauthToken),
		"refreshToken":       strings.TrimSpace(refreshToken),
		"needRefresh":        true,
		"authInfo":           map[string]any{},
	}
	return exchangeJobToken(ctx, inner, machine, centerURL, "refresh", doer)
}

func exchangeJobToken(ctx context.Context, inner map[string]any, machine *MachineIdentity, centerURL string, operation string, doer RequestDoer) (*AuthIdentity, error) {
	if centerURL == "" {
		centerURL = CenterBaseURL
	}
	if machine == nil {
		machine = NewMachine()
	}

	innerJSON, _ := json.Marshal(inner)
	outer := map[string]any{
		"payload":       string(innerJSON),
		"encodeVersion": "1",
	}
	outerJSON, _ := json.Marshal(outer)

	req, err := newCenterEncodedRequest("POST", centerURL+"/algo/api/v3/user/jobToken?Encode=1", outerJSON, machine)
	if err != nil {
		return nil, fmt.Errorf("qoder: %s request: %w", operation, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req = req.WithContext(ctx)

	if doer == nil {
		client := &http.Client{Timeout: 15 * time.Second}
		doer = client.Do
	}
	resp, err := doer(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: %s request: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: %s failed with status %d: %s", operation, resp.StatusCode, RedactSensitiveText(string(bodyBytes)))
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("qoder: parse %s response: %w", operation, err)
	}

	token, _ := data["securityOauthToken"].(string)
	refreshToken, _ := data["refreshToken"].(string)
	name, _ := data["name"].(string)
	uid, _ := data["id"].(string)
	userType, _ := data["userType"].(string)
	if userType == "" {
		userType = "personal_standard"
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("qoder: %s response missing securityOauthToken", operation)
	}

	return &AuthIdentity{
		Name:               name,
		AID:                uid,
		UID:                uid,
		UserType:           userType,
		SecurityOauthToken: strings.TrimSpace(token),
		RefreshToken:       strings.TrimSpace(refreshToken),
	}, nil
}

func newCenterEncodedRequest(method, rawURL string, payload []byte, machine *MachineIdentity) (*http.Request, error) {
	body := EncodeJSON(payload)
	req, err := http.NewRequest(method, rawURL, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Go-http-client/2.0")
	req.Header.Set("cosy-machinetoken", machine.MachineToken)
	req.Header.Set("cosy-machinetype", machine.MachineType)
	req.Header.Set("cosy-machineid", machine.MachineID)
	req.Header.Set("cosy-version", ClientVersion)
	req.Header.Set("cosy-clienttype", "5")
	req.Header.Set("login-version", "v2")
	req.Header.Set("appcode", AppCode)
	req.Header.Set("Date", date)
	req.Header.Set("signature", SignCenterRequest(date))
	return req, nil
}

// pathWithoutAlgo 移除 URL path 中用于签名计算之外的 "/algo" 前缀。
func pathWithoutAlgo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return strings.TrimPrefix(u.Path, "/algo")
}
