// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

func ParseCodexSessionImportEntries(req CodexSessionImportRequest) ([]CodexImportEntry, error) {
	contents := make([]string, 0, 1+len(req.Contents))
	if strings.TrimSpace(req.Content) != "" {
		contents = append(contents, req.Content)
	}
	for _, content := range req.Contents {
		if strings.TrimSpace(content) != "" {
			contents = append(contents, content)
		}
	}

	var entries []CodexImportEntry
	for _, content := range contents {
		values, err := parseCodexSessionImportContent(content)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			entries = append(entries, CodexImportEntry{
				Index: len(entries) + 1,
				Value: value,
			})
		}
	}
	return entries, nil
}

func parseCodexSessionImportContent(content string) ([]any, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, nil
	}

	if looksLikeJSON(trimmed) {
		values, err := decodeCodexJSONStream(trimmed)
		if err != nil {
			if strings.Contains(trimmed, "\n") {
				if lineValues, lineErr := parseCodexSessionImportLines(trimmed); lineErr == nil {
					return lineValues, nil
				}
			}
			return nil, fmt.Errorf("JSON 解析失败: %w", err)
		}
		return flattenCodexImportValues(values), nil
	}

	return parseCodexSessionImportLines(trimmed)
}

func parseCodexSessionImportLines(content string) ([]any, error) {
	values := make([]any, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if looksLikeJSON(line) {
			lineValues, err := decodeCodexJSONStream(line)
			if err != nil {
				return nil, fmt.Errorf("第 %d 行 JSON 解析失败: %w", len(values)+1, err)
			}
			values = append(values, flattenCodexImportValues(lineValues)...)
			continue
		}
		values = append(values, line)
	}
	return values, nil
}

func decodeCodexJSONStream(content string) ([]any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	values := make([]any, 0, 1)
	for {
		var value any
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, errors.New("空 JSON 内容")
	}
	return values, nil
}

func flattenCodexImportValues(values []any) []any {
	out := make([]any, 0, len(values))
	var appendValue func(any)
	appendValue = func(value any) {
		if arr, ok := value.([]any); ok {
			for _, item := range arr {
				appendValue(item)
			}
			return
		}
		out = append(out, value)
	}
	for _, value := range values {
		appendValue(value)
	}
	return out
}

func NormalizeCodexImportEntry(entry CodexImportEntry, options CodexImportOptions) (*CodexImportAccount, error) {
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	now := nowFunc().UTC()
	item := &CodexImportAccount{
		Credentials: map[string]any{},
		Extra: map[string]any{
			"import_source": "codex_session",
			"imported_at":   now.Format(time.RFC3339),
		},
	}

	switch raw := entry.Value.(type) {
	case string:
		item.AccessToken = strings.TrimSpace(raw)
	case map[string]any:
		if agentIdentity, ok := firstCodexMap(raw, []string{"agent_identity"}, []string{"agentIdentity"}); ok || strings.EqualFold(firstCodexString(raw, []string{"auth_mode"}, []string{"authMode"}), OpenAIAuthModeAgentIdentity) {
			if !ok {
				agentIdentity = raw
			}
			item.IsAgentIdentity = true
			item.AgentRuntimeID = firstCodexString(agentIdentity, []string{"agent_runtime_id"}, []string{"agentRuntimeId"})
			item.AgentPrivateKey = firstCodexString(agentIdentity, []string{"agent_private_key"}, []string{"agentPrivateKey"})
			item.AgentTaskID = firstCodexString(agentIdentity, []string{"task_id"}, []string{"taskId"})
			item.AccountID = firstCodexString(agentIdentity, []string{"account_id"}, []string{"accountId"})
			item.UserID = firstCodexString(agentIdentity, []string{"chatgpt_user_id"}, []string{"chatgptUserId"})
			item.Email = firstCodexString(agentIdentity, []string{"email"})
			item.PlanType = firstCodexString(agentIdentity, []string{"plan_type"}, []string{"planType"})
			item.AgentFedRAMP = firstCodexBool(agentIdentity, []string{"chatgpt_account_is_fedramp"}, []string{"chatgptAccountIsFedramp"})
			if item.AgentRuntimeID == "" || item.AgentPrivateKey == "" || item.AccountID == "" || item.UserID == "" {
				return nil, errors.New("agent identity 缺少必要字段")
			}
			if err := validateCodexPrivateKey(options.ValidatePrivateKey, item.AgentPrivateKey); err != nil {
				return nil, errors.New("agent identity private key 格式无效")
			}
			item.Credentials["auth_mode"] = OpenAIAuthModeAgentIdentity
			item.Credentials["agent_runtime_id"] = item.AgentRuntimeID
			item.Credentials["agent_private_key"] = item.AgentPrivateKey
			item.Credentials["chatgpt_account_id"] = item.AccountID
			item.Credentials["chatgpt_user_id"] = item.UserID
			item.Credentials["chatgpt_account_is_fedramp"] = item.AgentFedRAMP
			setCodexCredentialIfNotEmpty(item.Credentials, "task_id", item.AgentTaskID)
			setCodexCredentialIfNotEmpty(item.Credentials, "email", item.Email)
			setCodexCredentialIfNotEmpty(item.Credentials, "plan_type", item.PlanType)
			if item.AgentTaskID == "" {
				item.WarningTexts = append(item.WarningTexts, "未包含 task_id，首次请求会使用现有 runtime 注册新 task")
			}
			item.IdentityKeys = BuildCodexAgentIdentityKeys(item.AccountID)
			item.Name = buildCodexImportAccountName(item, entry.Index)
			return item, nil
		}
		item.AccessToken = firstCodexString(raw,
			[]string{"tokens", "access_token"},
			[]string{"tokens", "accessToken"},
			[]string{"access_token"},
			[]string{"accessToken"},
			[]string{"token"},
		)
		item.RefreshToken = firstCodexString(raw,
			[]string{"tokens", "refresh_token"},
			[]string{"tokens", "refreshToken"},
			[]string{"refresh_token"},
			[]string{"refreshToken"},
		)
		item.IDToken = firstCodexString(raw,
			[]string{"tokens", "id_token"},
			[]string{"tokens", "idToken"},
			[]string{"id_token"},
			[]string{"idToken"},
		)
		item.Email = firstCodexString(raw, []string{"email"}, []string{"user", "email"})
		item.AccountID = firstCodexString(raw,
			[]string{"chatgpt_account_id"},
			[]string{"chatgptAccountId"},
			[]string{"account_id"},
			[]string{"accountId"},
			[]string{"account", "id"},
			[]string{"account", "account_id"},
			[]string{"account", "chatgpt_account_id"},
		)
		item.UserID = firstCodexString(raw,
			[]string{"chatgpt_user_id"},
			[]string{"chatgptUserId"},
			[]string{"user_id"},
			[]string{"userId"},
			[]string{"user", "id"},
		)
		item.PlanType = firstCodexString(raw,
			[]string{"plan_type"},
			[]string{"planType"},
			[]string{"account", "plan_type"},
			[]string{"account", "planType"},
		)
		item.Organization = firstCodexString(raw,
			[]string{"organization_id"},
			[]string{"organizationId"},
			[]string{"org_id"},
			[]string{"orgId"},
		)
		item.Name = firstCodexString(raw, []string{"name"}, []string{"user", "name"})
		authProvider := firstCodexString(raw, []string{"auth_provider"}, []string{"authProvider"})
		if authProvider != "" {
			item.Extra["auth_provider"] = authProvider
		}
		if sessionToken := firstCodexString(raw, []string{"session_token"}, []string{"sessionToken"}); sessionToken != "" {
			item.Extra["session_token_present"] = true
			item.WarningTexts = append(item.WarningTexts, "sessionToken 已忽略，不会作为 OAuth refresh_token 存储")
		}
		if sessionExpiresAt, ok := codexTimeAt(raw, []string{"expires"}); ok {
			item.Extra["session_expires_at"] = sessionExpiresAt.Format(time.RFC3339)
		}
		if tokenExpiresAt, ok := firstCodexTime(raw,
			[]string{"tokens", "expires_at"},
			[]string{"tokens", "expiresAt"},
			[]string{"expires_at"},
			[]string{"expiresAt"},
		); ok {
			if tokenExpiresAt.Unix() <= now.Unix()-codexImportClockSkewSeconds {
				return nil, fmt.Errorf("access_token 已过期: %s", tokenExpiresAt.Format(time.RFC3339))
			}
			item.TokenExpiresAt = &tokenExpiresAt
			item.Credentials["expires_at"] = tokenExpiresAt.Format(time.RFC3339)
		}
		copyCodexExtraString(raw, item.Extra, "user_image", []string{"user", "image"})
		copyCodexExtraString(raw, item.Extra, "user_picture", []string{"user", "picture"})
		copyCodexExtraString(raw, item.Extra, "account_structure", []string{"account", "structure"})
		copyCodexExtraString(raw, item.Extra, "account_residency_region", []string{"account", "residencyRegion"})
		copyCodexExtraString(raw, item.Extra, "compute_residency", []string{"account", "computeResidency"})
	default:
		return nil, fmt.Errorf("第 %d 条格式不支持", entry.Index)
	}

	if item.IsAgentIdentity {
		return item, nil
	}
	if item.AccessToken == "" {
		return nil, errors.New("缺少 accessToken/access_token")
	}
	item.Credentials["access_token"] = item.AccessToken
	if item.RefreshToken != "" {
		item.Credentials["refresh_token"] = item.RefreshToken
		item.Credentials["client_id"] = options.OAuthClientID
	}
	if item.IDToken != "" {
		item.Credentials["id_token"] = item.IDToken
		_ = enrichCodexImportAccountFromJWT(item, item.IDToken, false, now)
	}
	if err := enrichCodexImportAccountFromJWT(item, item.AccessToken, true, now); err != nil {
		return nil, err
	}
	if _, ok := item.Credentials["expires_at"]; !ok {
		item.WarningTexts = append(item.WarningTexts, "无法从 accessToken 解析过期时间，导入后需自行确认令牌有效性")
	}
	if item.RefreshToken == "" {
		item.WarningTexts = append(item.WarningTexts, "未包含 refresh_token，accessToken 过期后无法自动续期")
	}

	setCodexCredentialIfNotEmpty(item.Credentials, "email", item.Email)
	setCodexCredentialIfNotEmpty(item.Credentials, "chatgpt_account_id", item.AccountID)
	setCodexCredentialIfNotEmpty(item.Credentials, "chatgpt_user_id", item.UserID)
	setCodexCredentialIfNotEmpty(item.Credentials, "organization_id", item.Organization)
	setCodexCredentialIfNotEmpty(item.Credentials, "plan_type", item.PlanType)

	fingerprint := CodexTokenFingerprint(item.AccessToken)
	item.Extra["access_token_sha256"] = fingerprint
	item.IdentityKeys = BuildCodexImportIdentityKeys(item.AccountID, item.UserID, item.Email, item.AccessToken, item.RefreshToken)
	item.Name = buildCodexImportAccountName(item, entry.Index)

	return item, nil
}

func enrichCodexImportAccountFromJWT(item *CodexImportAccount, token string, validateExpiry bool, now time.Time) error {
	claims, err := decodeCodexJWTClaims(token)
	if err != nil {
		if validateExpiry {
			item.WarningTexts = append(item.WarningTexts, "accessToken 不是可解析 JWT，无法校验过期时间和账号身份")
		}
		return nil
	}
	if validateExpiry && claims.Exp > 0 {
		if now.Unix() > claims.Exp+codexImportClockSkewSeconds {
			return fmt.Errorf("access_token 已过期: %s", time.Unix(claims.Exp, 0).UTC().Format(time.RFC3339))
		}
		expiresAt := time.Unix(claims.Exp, 0).UTC()
		item.TokenExpiresAt = &expiresAt
		item.Credentials["expires_at"] = expiresAt.Format(time.RFC3339)
	}
	if item.Email == "" {
		item.Email = strings.TrimSpace(claims.Email)
	}
	if claims.OpenAIAuth == nil {
		if item.UserID == "" {
			item.UserID = strings.TrimSpace(claims.Sub)
		}
		return nil
	}
	if item.AccountID == "" {
		item.AccountID = strings.TrimSpace(claims.OpenAIAuth.ChatGPTAccountID)
	}
	if item.UserID == "" {
		item.UserID = strings.TrimSpace(claims.OpenAIAuth.ChatGPTUserID)
	}
	if item.UserID == "" {
		item.UserID = strings.TrimSpace(claims.OpenAIAuth.UserID)
	}
	if item.PlanType == "" {
		item.PlanType = strings.TrimSpace(claims.OpenAIAuth.ChatGPTPlanType)
	}
	if item.Organization == "" {
		item.Organization = strings.TrimSpace(claims.OpenAIAuth.POID)
	}
	if item.Organization == "" {
		for _, org := range claims.OpenAIAuth.Organizations {
			if org.IsDefault {
				item.Organization = org.ID
				break
			}
		}
	}
	if item.Organization == "" && len(claims.OpenAIAuth.Organizations) > 0 {
		item.Organization = claims.OpenAIAuth.Organizations[0].ID
	}
	if item.UserID == "" {
		item.UserID = strings.TrimSpace(claims.Sub)
	}
	return nil
}

func decodeCodexJWTClaims(token string) (*CodexJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	payload, err := decodeCodexJWTSegment(parts[1])
	if err != nil {
		return nil, err
	}
	var claims CodexJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func decodeCodexJWTSegment(segment string) ([]byte, error) {
	if decoded, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(segment); err == nil {
		return decoded, nil
	}
	padded := segment
	if rem := len(padded) % 4; rem > 0 {
		padded += strings.Repeat("=", 4-rem)
	}
	if decoded, err := base64.URLEncoding.DecodeString(padded); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(padded)
}

func buildCodexImportAccountName(item *CodexImportAccount, index int) string {
	for _, candidate := range []string{item.Name, item.Email, item.AccountID, item.UserID} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return fmt.Sprintf("Codex 导入账号 %d", index)
}

func buildCodexCreateAccountName(base string, item *CodexImportAccount, index, total int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		if item == nil {
			return fmt.Sprintf("Codex 导入账号 %d", index)
		}
		return item.Name
	}
	if total > 1 {
		return fmt.Sprintf("%s #%d", base, index)
	}
	return base
}

func ResolveCodexImportExpiry(req CodexSessionImportRequest, item *CodexImportAccount, now func() time.Time) (*int64, *time.Time, *bool, []string, error) {
	if item == nil {
		return nil, nil, nil, nil, errors.New("导入项为空")
	}
	// Agent Identity 没有 OAuth access token 有效期，其 runtime/task 生命周期由上游恢复链处理。
	// 因此导入时不能套用 OAuth 过期拒绝或自动暂停策略。
	if item.IsAgentIdentity {
		return nil, nil, nil, nil, nil
	}

	var requestExpiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt > 0 {
		t := time.Unix(*req.ExpiresAt, 0).UTC()
		requestExpiresAt = &t
	}

	var accountExpiresAt *time.Time
	var credentialExpiresAt *time.Time
	warnings := make([]string, 0, 2)
	if item.RefreshToken == "" {
		if item.TokenExpiresAt != nil {
			tokenExpiresAt := item.TokenExpiresAt.UTC()
			accountExpiresAt = &tokenExpiresAt
			credentialExpiresAt = &tokenExpiresAt
		}
		if requestExpiresAt != nil {
			accountExpiresAt = earlierCodexTime(accountExpiresAt, requestExpiresAt)
			credentialExpiresAt = earlierCodexTime(credentialExpiresAt, requestExpiresAt)
		}
		if accountExpiresAt == nil {
			return nil, nil, nil, nil, errors.New("未包含 refresh_token，且无法解析 accessToken 过期时间；请在第一步设置过期时间后再导入")
		}
		if accountExpiresAt.Unix() <= now().UTC().Unix()-codexImportClockSkewSeconds {
			return nil, nil, nil, nil, fmt.Errorf("过期时间已过期: %s", accountExpiresAt.Format(time.RFC3339))
		}
		warnings = append(warnings, "未包含 refresh_token，已按 accessToken/账号过期时间设置自动停止调度")
		if req.AutoPauseOnExpired != nil && !*req.AutoPauseOnExpired {
			warnings = append(warnings, "未包含 refresh_token，已强制开启过期自动暂停")
		}
		autoPause := true
		expiresAtUnix := accountExpiresAt.Unix()
		return &expiresAtUnix, credentialExpiresAt, &autoPause, warnings, nil
	}

	if requestExpiresAt != nil {
		accountExpiresAt = requestExpiresAt
	}
	if item.TokenExpiresAt != nil {
		tokenExpiresAt := item.TokenExpiresAt.UTC()
		credentialExpiresAt = &tokenExpiresAt
	}
	var expiresAtUnix *int64
	if accountExpiresAt != nil {
		v := accountExpiresAt.Unix()
		expiresAtUnix = &v
	}
	return expiresAtUnix, credentialExpiresAt, req.AutoPauseOnExpired, warnings, nil
}

func earlierCodexTime(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return current
	}
	if current == nil || candidate.Before(*current) {
		t := candidate.UTC()
		return &t
	}
	t := current.UTC()
	return &t
}

func SanitizeCodexImportCredentialExtras(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	protected := map[string]struct{}{
		"access_token":               {},
		"refresh_token":              {},
		"id_token":                   {},
		"expires_at":                 {},
		"email":                      {},
		"chatgpt_account_id":         {},
		"chatgpt_user_id":            {},
		"organization_id":            {},
		"plan_type":                  {},
		"client_id":                  {},
		"auth_mode":                  {},
		"openai_auth_mode":           {},
		"token_type":                 {},
		"chatgpt_account_is_fedramp": {},
		"agent_runtime_id":           {},
		"agent_private_key":          {},
		"task_id":                    {},
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			continue
		}
		if _, ok := protected[strings.ToLower(normalizedKey)]; ok {
			continue
		}
		out[normalizedKey] = value
	}
	if len(out) == 0 {
		return nil
	}
	return CloneValues(out)
}

// BuildCodexImportIdentityKeys 生成导入条目的匹配键。refresh_token 缺失时
// Codex session 只能作为 accessToken-only 凭据使用，此时以 access token
// 指纹作为唯一稳定身份，避免同 workspace 下共享的 account/user 标识误合并。
func BuildCodexImportIdentityKeys(accountID, userID, email, accessToken, refreshToken string) []string {
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" && accessToken != "" {
		return []string{"access:" + CodexTokenFingerprint(accessToken)}
	}
	return BuildCodexStoredIdentityKeys(accountID, userID, email, accessToken)
}

func BuildCodexAgentIdentityKeys(accountID string) []string {
	// 同一 ChatGPT 账号下的 Agent Identity 凭据应合并，但同一用户可能拥有多个 Team
	// 账号。这里不能用 user、email 或 runtime 作为回退键：user_id 会跨 Team 共享，
	// runtime_id 则会在同一账号注册新 runtime 时变化。
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil
	}
	return []string{"account:" + accountID}
}

func MergeCodexImportMap(existing, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(existing)+len(incoming))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range incoming {
		out[k] = v
	}
	return CloneValues(out)
}

func MergeCodexImportCredentials(existing, incoming map[string]any, item *CodexImportAccount) map[string]any {
	out := MergeCodexImportMap(existing, incoming)
	if item == nil {
		return out
	}
	if strings.TrimSpace(item.RefreshToken) == "" {
		if CodexCredentialString(existing, "refresh_token") == "" {
			delete(out, "refresh_token")
			delete(out, "client_id")
		} else {
			out["refresh_token"] = existing["refresh_token"]
			if clientID, ok := existing["client_id"]; ok {
				out["client_id"] = clientID
			}
		}
	}
	if strings.TrimSpace(item.IDToken) == "" {
		delete(out, "id_token")
	}
	return out
}

func CodexCredentialString(credentials map[string]any, key string) string {
	if credentials == nil {
		return ""
	}
	return codexStringValue(credentials[key])
}

func CodexTokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func looksLikeJSON(content string) bool {
	if content == "" {
		return false
	}
	switch content[0] {
	case '{', '[':
		return true
	default:
		return false
	}
}

func firstCodexString(obj map[string]any, paths ...[]string) string {
	for _, path := range paths {
		if value, ok := codexPathValue(obj, path); ok {
			if str := codexStringValue(value); str != "" {
				return str
			}
		}
	}
	return ""
}

func firstCodexMap(obj map[string]any, paths ...[]string) (map[string]any, bool) {
	for _, path := range paths {
		value, ok := codexPathValue(obj, path)
		if !ok || value == nil {
			continue
		}
		if mapped, ok := value.(map[string]any); ok {
			return mapped, true
		}
	}
	return nil, false
}

func firstCodexBool(obj map[string]any, paths ...[]string) bool {
	for _, path := range paths {
		value, ok := codexPathValue(obj, path)
		if !ok {
			continue
		}
		switch value := value.(type) {
		case bool:
			return value
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(value))
			if err == nil {
				return parsed
			}
		}
	}
	return false
}

func copyCodexExtraString(obj map[string]any, extra map[string]any, key string, path []string) {
	value := firstCodexString(obj, path)
	if value != "" {
		extra[key] = value
	}
}

func firstCodexTime(obj map[string]any, paths ...[]string) (time.Time, bool) {
	for _, path := range paths {
		if value, ok := codexTimeAt(obj, path); ok {
			return value, true
		}
	}
	return time.Time{}, false
}

func codexTimeAt(obj map[string]any, path []string) (time.Time, bool) {
	value, ok := codexPathValue(obj, path)
	if !ok {
		return time.Time{}, false
	}
	return parseCodexTimeValue(value)
}

func codexPathValue(obj map[string]any, path []string) (any, bool) {
	var current any = obj
	for _, key := range path {
		currentObj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := currentObj[key]
		if !ok {
			return nil, false
		}
		current = value
	}
	return current, true
}

func codexStringValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case float64:
		return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
	case float32:
		return strings.TrimSpace(strconv.FormatFloat(float64(v), 'f', -1, 32))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

func setCodexCredentialIfNotEmpty(credentials map[string]any, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		credentials[key] = value
	}
}

func parseCodexTimeValue(value any) (time.Time, bool) {
	switch v := value.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return time.Time{}, false
		}
		if parsed, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return parsed.UTC(), true
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return codexUnixTime(n), true
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return codexUnixTime(n), true
		}
		if f, err := v.Float64(); err == nil {
			return codexUnixTime(int64(f)), true
		}
	case float64:
		return codexUnixTime(int64(v)), true
	case int:
		return codexUnixTime(int64(v)), true
	case int64:
		return codexUnixTime(v), true
	}
	return time.Time{}, false
}

func codexUnixTime(value int64) time.Time {
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

// 未配置技术校验时失败关闭，不能把 Agent Identity 私钥当成普通字符串接受。
func validateCodexPrivateKey(validate func(string) error, key string) error {
	if validate == nil {
		return errors.New("agent identity private key validator is not configured")
	}
	return validate(key)
}
