package messageforward

import (
	"context"
	"strconv"
	"strings"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/tidwall/gjson"
)

// metadataUserID 保留账号与客户端会话共同派生的 OAuth 身份。
func metadataUserID(parsed *requeststate.ParsedRequest, account *gatewayprovider.ExecutionAccount, fp *claude.Fingerprint) string {
	if parsed == nil || account == nil {
		return ""
	}
	if parsed.MetadataUserID != "" {
		return ""
	}

	userID := strings.TrimSpace(account.View().GetClaudeUserID())
	if userID == "" && fp != nil {
		userID = fp.ClientID
	}
	if userID == "" {
		// 账号元数据不完整时生成格式有效的客户端 ID。
		userID = claude.GenerateClientID()
	}

	// session_id 用"会话级稳定种子"派生（账号 + 客户端区分因子 + 首条 user 文本）：
	// 随对话在尾部追加 messages 时保持不变，贴近真实 CC 进程级稳定的 session_id。
	// 不复用 GenerateSessionHash —— 后者是粘性路由键、按设计逐轮变化（见其测试）。
	var firstUserText string
	if parsed.Body != nil {
		firstUserText = claude.ExtractFirstUserText(parsed.Body.Bytes())
	}
	seed := claude.BuildStableSessionSeed(account.Record.ID, sessionContextDiscriminator(parsed.SessionContext), firstUserText)
	sessionID := upstream.GenerateSessionUUID(seed)

	// 根据指纹 UA 版本选择输出格式
	var uaVersion string
	if fp != nil {
		uaVersion = claude.ExtractCLIVersion(fp.UserAgent)
	}
	accountUUID := strings.TrimSpace(account.View().GetExtraString("account_uuid"))
	return claude.FormatMetadataUserID(userID, accountUUID, sessionID, uaVersion)
}

func metadataUserIDFromBody(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	fp *claude.Fingerprint,
	body []byte,
) string {
	_ = ctx
	if account == nil {
		return ""
	}
	if existing := gjson.GetBytes(body, "metadata.user_id").String(); existing != "" {
		return ""
	}

	userID := strings.TrimSpace(account.View().GetClaudeUserID())
	if userID == "" && fp != nil {
		userID = fp.ClientID
	}
	if userID == "" {
		userID = claude.GenerateClientID()
	}

	// 与 buildOAuthMetadataUserID 一致：用会话级稳定种子，避免整 body 哈希导致
	// 每轮（甚至每个 token 变化）都重算出不同的 session_id。
	var clientDiscriminator string
	if fp != nil {
		clientDiscriminator = fp.ClientID
	}
	seed := claude.BuildStableSessionSeed(account.Record.ID, clientDiscriminator, claude.ExtractFirstUserText(body))
	sessionID := upstream.GenerateSessionUUID(seed)

	var uaVersion string
	if fp != nil {
		uaVersion = claude.ExtractCLIVersion(fp.UserAgent)
	}
	accountUUID := strings.TrimSpace(account.View().GetExtraString("account_uuid"))
	return claude.FormatMetadataUserID(userID, accountUUID, sessionID, uaVersion)
}

// sessionContextDiscriminator 把请求上下文（客户端 IP / 归一化 UA / API Key ID）拼成
// 一个跨客户端的区分因子，避免不同用户的相同首条消息派生出相同 session_id。
func sessionContextDiscriminator(sc *requeststate.SessionContext) string {
	if sc == nil {
		return ""
	}
	return sc.ClientIP + ":" + requeststate.NormalizeSessionUserAgent(sc.UserAgent) + ":" + strconv.FormatInt(sc.APIKeyID, 10)
}
