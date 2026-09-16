// 本文件组合原生请求；URL/账户政策由调用方投影，不接收旧账号或 Gin。
package anthropic

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/tidwall/gjson"
)

type RequestOptions struct {
	OriginalPolicy                   func(context.Context, string, string) (map[string]struct{}, error)
	InjectAPIKeyBeta                 bool
	AccountID                        int64
	OAuth, MaskSession, APIKeyBearer bool
	AccountUUID                      string
	ClientHeaders                    http.Header
	Fingerprint                      *RequestFingerprint
	URL                              func() (string, error)
	FastMode                         func(context.Context, []byte, http.Header) ([]byte, http.Header, error)
	Forwarding                       func(context.Context) (bool, bool)
	FilterSet                        func(context.Context) map[string]struct{}
	BetaOverride                     func() (string, bool)
	CheckFastBeta                    func(context.Context) error
	ApplyOverrides                   func(http.Header)
	Debug                            func(*http.Request, []byte, map[string]string)
	Capture                          func(*http.Request, []byte)
}

func SetAPIKeyAuthHeader(header http.Header, bearer bool, token string) {
	if bearer {
		SetHeaderRaw(header, "authorization", "Bearer "+token)
	} else {
		SetHeaderRaw(header, "x-api-key", token)
	}
}
func BuildRequest(ctx context.Context, body []byte, token, tokenType, modelID string, reqStream, mimicClaudeCode bool, options RequestOptions) (*http.Request, []byte, error) {
	body = StripDeferredToolCacheControl(body)
	targetURL, err := options.URL()
	if err != nil {
		return nil, nil, err
	}
	clientHeaders := options.ClientHeaders
	body, clientHeaders, err = options.FastMode(ctx, body, clientHeaders)
	if err != nil {
		return nil, nil, err
	}

	// OAuth账号：应用统一指纹和metadata重写（受设置开关控制）
	var fingerprint *Fingerprint
	enableFP, enableMPT := true, false
	if options.Forwarding != nil {
		enableFP, enableMPT = options.Forwarding(ctx)
	}
	if options.OAuth && options.Fingerprint != nil {
		// 1. 获取或创建指纹（包含随机生成的ClientID）
		fp, err := options.Fingerprint.GetOrCreateFingerprint(ctx, options.AccountID, clientHeaders)
		if err != nil {
			logger.LegacyPrintf("service.gateway", "Warning: failed to get fingerprint for account %d: %v", options.AccountID, err)
			// 失败时降级为透传原始headers
		} else {
			if enableFP {
				fingerprint = fp
			}

			// 2. 重写metadata.user_id（需要指纹中的ClientID和账号的account_uuid）
			// 如果启用了会话ID伪装，会在重写后替换 session 部分为固定值
			// 当 metadata 透传开启时跳过重写
			if !enableMPT {
				accountUUID := options.AccountUUID
				if accountUUID != "" && fp.ClientID != "" {
					if newBody, err := options.Fingerprint.RewriteUserIDWithMasking(ctx, body, options.AccountID, options.MaskSession, accountUUID, fp.ClientID, fp.UserAgent); err == nil && len(newBody) > 0 {
						body = newBody
					}
				}
			}
		}
	}

	// 后续伪装会覆盖缓存 UA；即使没有账号指纹，也按最终出站 UA 同步计费标记。
	if billingUA := EffectiveBillingUserAgent(tokenType, mimicClaudeCode, fingerprint); billingUA != "" {
		body = SyncBillingHeaderVersion(body, billingUA)
	}

	// === 计算最终 anthropic-beta header（先于 body sanitize）===
	//
	// 顺序约束：
	//   1) 算 finalBeta（纯函数，不依赖 req.Header；mimicry 路径会忽略客户端 beta，
	//      与原“OAuth + mimicClaudeCode 跳过白名单透传”行为对齐）
	//   2) 按 finalBeta 做能力维度 body sanitize（如 context-management beta 缺失 →
	//      strip body.context_management，与 Bedrock 路径对称）
	//   3) NewRequest（body 至此最终敲定；新版 CLI 已取消 cch 签名字段）
	//   4) 透传白名单 / fingerprint / mimic header / 写入 finalBeta
	policyFilterSet := options.FilterSet(ctx)
	effectiveDropSet := MergeDropSets(policyFilterSet)
	finalBetaHeader, finalBetaShouldSet := ComputeFinalAnthropicBeta(
		tokenType, mimicClaudeCode, modelID, clientHeaders, body, effectiveDropSet, options.InjectAPIKeyBeta,
	)

	// 账号覆写了 anthropic-beta 时，覆写值即最终上游值（由下方 ApplyHeaderOverrides 写入）：
	// body 能力净化必须以覆写值为准，否则 header/body 不对称会被上游 400。
	if beta, ok := options.BetaOverride(); ok {
		finalBetaHeader, finalBetaShouldSet = beta, true
	}
	if ContainsBetaToken(finalBetaHeader, BetaFastMode) {
		if blockErr := options.CheckFastBeta(ctx); blockErr != nil {
			return nil, nil, blockErr
		}
	}

	// 能力维度 body sanitize：与最终 anthropic-beta header 对称
	if sanitized, changed := SanitizeAnthropicBodyForBetaTokens(body, finalBetaHeader); changed {
		body = sanitized
	}

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	// 设置认证头（保持原始大小写）
	if tokenType == "oauth" {
		SetHeaderRaw(req.Header, "authorization", "Bearer "+token)
	} else {
		SetAPIKeyAuthHeader(req.Header, options.APIKeyBearer, token)
	}

	// 白名单透传 headers
	// OAuth mimicry 路径：跳过客户端 header 透传，与 Parrot 对齐。
	// Parrot 的 build_upstream_headers 只发 9 个精确 header，不透传任何客户端 header。
	// 透传客户端 header 会引入不一致的 x-stainless-* / anthropic-beta / user-agent /
	// x-claude-code-session-id 等值，和我们注入的伪装 header 冲突，被 Anthropic 判 third-party。
	if tokenType != "oauth" || !mimicClaudeCode {
		for key, values := range clientHeaders {
			lowerKey := strings.ToLower(key)
			if AllowedHeaders[lowerKey] {
				wireKey := ResolveWireCasing(key)
				for _, v := range values {
					AddHeaderRaw(req.Header, wireKey, v)
				}
			}
		}
	}

	// OAuth账号：应用缓存的指纹到请求头（覆盖白名单透传的头）
	if fingerprint != nil {
		options.Fingerprint.ApplyFingerprint(req, fingerprint)
	}

	// 确保必要的headers存在（保持原始大小写）
	if GetHeaderRaw(req.Header, "content-type") == "" {
		SetHeaderRaw(req.Header, "content-type", "application/json")
	}
	if GetHeaderRaw(req.Header, "anthropic-version") == "" {
		SetHeaderRaw(req.Header, "anthropic-version", "2023-06-01")
	}
	if tokenType == "oauth" {
		ApplyClaudeOAuthHeaderDefaults(req)
	}

	// OAuth + mimic Claude Code：强制注入 CLI 指纹相关 header
	// （user-agent/x-stainless-*/x-app/Accept/x-stainless-helper-method/x-client-request-id）
	if tokenType == "oauth" && mimicClaudeCode {
		ApplyClaudeCodeMimicHeaders(req, reqStream)
	}

	// 写入最终 anthropic-beta header
	// 注：透传分支白名单可能写入了客户端 anthropic-beta，无条件 Del 一次再按 finalBeta
	// 决定是否 set，确保 dropSet 过滤后的结果一定覆盖客户端原始值。
	DeleteHeaderAllForms(req.Header, "anthropic-beta")
	if finalBetaShouldSet {
		SetHeaderRaw(req.Header, "anthropic-beta", finalBetaHeader)
	}

	// 同步 X-Claude-Code-Session-Id 头：取 body 中已处理的 metadata.user_id 的 session_id 覆盖
	if sessionHeader := GetHeaderRaw(req.Header, "X-Claude-Code-Session-Id"); sessionHeader != "" {
		if uid := gjson.GetBytes(body, "metadata.user_id").String(); uid != "" {
			if parsed := ParseMetadataUserID(uid); parsed != nil {
				SetHeaderRaw(req.Header, "X-Claude-Code-Session-Id", parsed.SessionID)
			}
		}
	}

	// 账号级请求头覆写（仅 anthropic/openai api_key 账号启用时生效；OAuth 路径 no-op）。
	// 放在所有 header 逻辑之后，确保配置值对同名头拥有最终决定权。
	options.ApplyOverrides(req.Header)

	if options.Debug != nil {
		options.Debug(req, body, map[string]string{"url": req.URL.String(), "token_type": tokenType, "mimic_claude_code": strconv.FormatBool(mimicClaudeCode), "fingerprint_applied": strconv.FormatBool(fingerprint != nil), "enable_fp": strconv.FormatBool(enableFP), "enable_mpt": strconv.FormatBool(enableMPT)})
	}
	if options.Capture != nil {
		options.Capture(req, body)
	}
	return req, body, nil
}
