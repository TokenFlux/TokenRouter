package account

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseCodexSessionImportEntriesSupportsRawTokenJSONAndArray(t *testing.T) {
	token1 := "raw-access-token-1"
	token2 := buildOriginalCodexTestJWT(t, time.Now().Add(time.Hour), map[string]any{
		"email": "json@example.com",
	})
	token3 := "raw-access-token-3"

	req := CodexSessionImportRequest{
		Content: fmt.Sprintf("%s\n{\"accessToken\":%q}\n[%q]", token1, token2, token3),
	}

	entries, err := ParseCodexSessionImportEntries(req)
	if err != nil {
		t.Fatalf("ParseCodexSessionImportEntries error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	first, err := NormalizeCodexImportEntry(entries[0], CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("normalize raw token error = %v", err)
	}
	if first.Credentials["access_token"] != token1 {
		t.Fatalf("raw token access_token = %v, want %s", first.Credentials["access_token"], token1)
	}

	second, err := NormalizeCodexImportEntry(entries[1], CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("normalize json token error = %v", err)
	}
	if second.Email != "json@example.com" {
		t.Fatalf("email = %q, want json@example.com", second.Email)
	}

	third, err := NormalizeCodexImportEntry(entries[2], CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("normalize array token error = %v", err)
	}
	if third.Credentials["access_token"] != token3 {
		t.Fatalf("array token access_token = %v, want %s", third.Credentials["access_token"], token3)
	}
}

func TestParseCodexSessionImportEntriesFallsBackToLineModeForMixedJSONAndToken(t *testing.T) {
	req := CodexSessionImportRequest{
		Content: "{\"accessToken\":\"json-line-token\"}\nraw-line-token",
	}

	entries, err := ParseCodexSessionImportEntries(req)
	if err != nil {
		t.Fatalf("ParseCodexSessionImportEntries error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	first, err := NormalizeCodexImportEntry(entries[0], CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("normalize json line error = %v", err)
	}
	if first.Credentials["access_token"] != "json-line-token" {
		t.Fatalf("json line access_token = %v, want json-line-token", first.Credentials["access_token"])
	}

	second, err := NormalizeCodexImportEntry(entries[1], CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("normalize raw line error = %v", err)
	}
	if second.Credentials["access_token"] != "raw-line-token" {
		t.Fatalf("raw line access_token = %v, want raw-line-token", second.Credentials["access_token"])
	}
}

func TestNormalizeCodexSessionJSONExtractsCredentialsAndIgnoresSessionToken(t *testing.T) {
	accessToken := buildOriginalCodexTestJWT(t, time.Now().Add(time.Hour), map[string]any{
		"email": "claim@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-claim",
			"chatgpt_user_id":    "user-from-claim",
			"chatgpt_plan_type":  "plus",
			"poid":               "org-from-claim",
		},
	})
	raw := map[string]any{
		"user": map[string]any{
			"id":    "user-from-json",
			"name":  "Sup OO",
			"email": "json@example.com",
			"image": "https://example.com/avatar.png",
		},
		"account": map[string]any{
			"id":       "acct-from-json",
			"planType": "free",
		},
		"accessToken":  accessToken,
		"sessionToken": "secret-session-token",
		"expires":      "2026-08-05T13:40:42.836Z",
	}

	item, err := NormalizeCodexImportEntry(CodexImportEntry{Index: 1, Value: raw}, CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("NormalizeCodexImportEntry error = %v", err)
	}
	if item.Credentials["access_token"] != accessToken {
		t.Fatalf("access_token not stored")
	}
	if item.Credentials["email"] != "json@example.com" {
		t.Fatalf("email = %v, want json@example.com", item.Credentials["email"])
	}
	if item.Credentials["chatgpt_account_id"] != "acct-from-json" {
		t.Fatalf("chatgpt_account_id = %v, want acct-from-json", item.Credentials["chatgpt_account_id"])
	}
	if item.Credentials["chatgpt_user_id"] != "user-from-json" {
		t.Fatalf("chatgpt_user_id = %v, want user-from-json", item.Credentials["chatgpt_user_id"])
	}
	if item.Credentials["plan_type"] != "free" {
		t.Fatalf("plan_type = %v, want free", item.Credentials["plan_type"])
	}
	if _, ok := item.Credentials["session_token"]; ok {
		t.Fatalf("session_token should not be written to credentials")
	}
	if item.Extra["session_token_present"] != true {
		t.Fatalf("session_token_present = %v, want true", item.Extra["session_token_present"])
	}
	if item.Extra["session_expires_at"] != "2026-08-05T13:40:42Z" {
		t.Fatalf("session_expires_at = %v", item.Extra["session_expires_at"])
	}
	if item.TokenExpiresAt == nil {
		t.Fatalf("TokenExpiresAt should be parsed from accessToken")
	}
}

func TestMergeCodexImportCredentialsPreservesExistingRefreshFieldsWhenIncomingHasNoRefreshToken(t *testing.T) {
	existing := map[string]any{
		"access_token":       "old-access-token",
		"refresh_token":      "old-refresh-token",
		"client_id":          "old-client-id",
		"id_token":           "old-id-token",
		"model_mapping":      map[string]any{"from": "existing"},
		"chatgpt_account_id": "acct-old",
		"unrelated_existing": "keep",
	}
	incoming := map[string]any{
		"access_token":       "new-access-token",
		"expires_at":         "2026-08-05T13:40:42Z",
		"chatgpt_account_id": "acct-new",
	}
	item := &CodexImportAccount{
		AccessToken: "new-access-token",
	}

	merged := MergeCodexImportCredentials(existing, incoming, item)

	if merged["access_token"] != "new-access-token" {
		t.Fatalf("access_token = %v, want new-access-token", merged["access_token"])
	}
	if merged["chatgpt_account_id"] != "acct-new" {
		t.Fatalf("chatgpt_account_id = %v, want acct-new", merged["chatgpt_account_id"])
	}
	if merged["refresh_token"] != "old-refresh-token" {
		t.Fatalf("refresh_token = %v, want old-refresh-token", merged["refresh_token"])
	}
	if merged["client_id"] != "old-client-id" {
		t.Fatalf("client_id = %v, want old-client-id", merged["client_id"])
	}
	if _, ok := merged["id_token"]; ok {
		t.Fatalf("id_token should be cleared")
	}
	if merged["unrelated_existing"] != "keep" {
		t.Fatalf("unrelated_existing = %v, want keep", merged["unrelated_existing"])
	}
	if _, ok := merged["model_mapping"]; !ok {
		t.Fatalf("model_mapping should be preserved")
	}
}

func TestMergeCodexImportCredentialsKeepsRefreshFieldsWhenIncomingHasRefreshToken(t *testing.T) {
	existing := map[string]any{
		"refresh_token": "old-refresh-token",
		"client_id":     "old-client-id",
		"id_token":      "old-id-token",
	}
	incoming := map[string]any{
		"access_token":  "new-access-token",
		"refresh_token": "new-refresh-token",
		"client_id":     "new-client-id",
		"id_token":      "new-id-token",
	}
	item := &CodexImportAccount{
		AccessToken:  "new-access-token",
		RefreshToken: "new-refresh-token",
		IDToken:      "new-id-token",
	}

	merged := MergeCodexImportCredentials(existing, incoming, item)

	if merged["refresh_token"] != "new-refresh-token" {
		t.Fatalf("refresh_token = %v, want new-refresh-token", merged["refresh_token"])
	}
	if merged["client_id"] != "new-client-id" {
		t.Fatalf("client_id = %v, want new-client-id", merged["client_id"])
	}
	if merged["id_token"] != "new-id-token" {
		t.Fatalf("id_token = %v, want new-id-token", merged["id_token"])
	}
}

func TestNormalizeCodexImportRejectsExpiredAccessToken(t *testing.T) {
	expiredToken := buildOriginalCodexTestJWT(t, time.Now().Add(-time.Hour), map[string]any{})

	_, err := NormalizeCodexImportEntry(CodexImportEntry{Index: 1, Value: expiredToken}, CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err == nil {
		t.Fatal("NormalizeCodexImportEntry error = nil, want expired token error")
	}
	if !strings.Contains(err.Error(), "已过期") {
		t.Fatalf("error = %v, want expired token message", err)
	}
}

func TestResolveCodexImportExpiryForNoRefreshTokenUsesTokenExpiry(t *testing.T) {
	tokenExpiresAt := time.Now().Add(time.Hour).UTC()
	item := &CodexImportAccount{
		AccessToken:    "access-token",
		Credentials:    map[string]any{"access_token": "access-token"},
		TokenExpiresAt: &tokenExpiresAt,
		WarningTexts:   []string{},
	}
	disabled := false
	req := CodexSessionImportRequest{AutoPauseOnExpired: &disabled}

	accountExpiresAt, credentialExpiresAt, autoPause, warnings, err := ResolveCodexImportExpiry(req, item, time.Now)
	if err != nil {
		t.Fatalf("ResolveCodexImportExpiry error = %v", err)
	}
	if accountExpiresAt == nil || *accountExpiresAt != tokenExpiresAt.Unix() {
		t.Fatalf("account expires_at = %v, want %d", accountExpiresAt, tokenExpiresAt.Unix())
	}
	if credentialExpiresAt == nil || credentialExpiresAt.Unix() != tokenExpiresAt.Unix() {
		t.Fatalf("credential expires_at = %v, want %s", credentialExpiresAt, tokenExpiresAt)
	}
	if autoPause == nil || !*autoPause {
		t.Fatalf("autoPause = %v, want true", autoPause)
	}
	if len(warnings) == 0 {
		t.Fatalf("warnings should not be empty")
	}
}

func TestResolveCodexImportExpiryForNoRefreshTokenRequiresExpiry(t *testing.T) {
	item := &CodexImportAccount{
		AccessToken:  "opaque-access-token",
		Credentials:  map[string]any{"access_token": "opaque-access-token"},
		WarningTexts: []string{},
	}

	_, _, _, _, err := ResolveCodexImportExpiry(CodexSessionImportRequest{}, item, time.Now)
	if err == nil {
		t.Fatal("ResolveCodexImportExpiry error = nil, want missing expiry error")
	}
	if !strings.Contains(err.Error(), "无法解析 accessToken 过期时间") {
		t.Fatalf("error = %v, want missing expiry message", err)
	}
}

func TestResolveCodexImportExpiryForNoRefreshTokenUsesEarlierRequestExpiry(t *testing.T) {
	tokenExpiresAt := time.Now().Add(2 * time.Hour).UTC()
	requestExpiresAt := time.Now().Add(time.Hour).UTC()
	item := &CodexImportAccount{
		AccessToken:    "access-token",
		Credentials:    map[string]any{"access_token": "access-token"},
		TokenExpiresAt: &tokenExpiresAt,
		WarningTexts:   []string{},
	}
	reqUnix := requestExpiresAt.Unix()
	req := CodexSessionImportRequest{ExpiresAt: &reqUnix}

	accountExpiresAt, credentialExpiresAt, _, _, err := ResolveCodexImportExpiry(req, item, time.Now)
	if err != nil {
		t.Fatalf("ResolveCodexImportExpiry error = %v", err)
	}
	if accountExpiresAt == nil || *accountExpiresAt != requestExpiresAt.Unix() {
		t.Fatalf("account expires_at = %v, want %d", accountExpiresAt, requestExpiresAt.Unix())
	}
	if credentialExpiresAt == nil || credentialExpiresAt.Unix() != requestExpiresAt.Unix() {
		t.Fatalf("credential expires_at = %v, want %s", credentialExpiresAt, requestExpiresAt)
	}
}

func TestCodexIdentityKeysPreferStrongIdentifiers(t *testing.T) {
	keys := BuildCodexImportIdentityKeys("acct-1", "user-1", "same@example.com", "token", "refresh")
	if len(keys) == 0 || keys[0] != "user:user-1" {
		t.Fatalf("user key should have highest priority when refresh token exists: %v", keys)
	}
	if keys[len(keys)-1] != "account:acct-1" {
		t.Fatalf("shared account key should be the last fallback: %v", keys)
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "email:") {
			t.Fatalf("strong identity should not include email fallback: %v", keys)
		}
	}

	keys = BuildCodexImportIdentityKeys("", "", "same@example.com", "token", "refresh")
	hasEmail := false
	for _, key := range keys {
		if key == "email:same@example.com" {
			hasEmail = true
		}
	}
	if !hasEmail {
		t.Fatalf("weak identity should include email fallback: %v", keys)
	}

	keys = BuildCodexImportIdentityKeys("acct-1", "user-1", "same@example.com", "token", "")
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "access:") {
		t.Fatalf("accessToken-only identity should use only access fingerprint: %v", keys)
	}
}

func TestCodexAccountIndexDoesNotMatchDifferentUsersInSameChatGPTAccount(t *testing.T) {
	existing := Record{
		ID: 10,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       "token-1",
			"refresh_token":      "refresh-1",
		},
	}
	index := BuildCodexAccountIndex([]Record{existing})

	keys := BuildCodexImportIdentityKeys("team-1", "user-2", "", "token-2", "refresh-2")
	if got, _ := index.Find(keys, "user-2"); got != nil {
		t.Fatalf("Find matched account ID %d for a different chatgpt_user_id in the same team", got.ID)
	}

	keys = BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-2", "refresh-2")
	got, _ := index.Find(keys, "user-1")
	if got == nil || got.ID != existing.ID {
		t.Fatalf("Find by same chatgpt_user_id = %v, want account ID %d", got, existing.ID)
	}
}

func TestCodexAccountIndexFallsBackToAccountKeyWhenRefreshTokenExistsAndUserIDMissing(t *testing.T) {
	// 含 refresh_token 的常规导入沿用 a5638a4e 的兼容逻辑：存量账号缺少
	// chatgpt_user_id 时，携带 user id 的重新导入仍可命中并回填。
	legacy := Record{
		ID: 20,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"access_token":       "token-old",
			"refresh_token":      "refresh-old",
		},
	}
	index := BuildCodexAccountIndex([]Record{legacy})

	keys := BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-new", "refresh-new")
	got, matchedKey := index.Find(keys, "user-1")
	if got == nil || got.ID != legacy.ID {
		t.Fatalf("Find legacy account without stored user id = %v, want account ID %d", got, legacy.ID)
	}
	if matchedKey != "account:team-1" {
		t.Fatalf("matched key = %q, want account:team-1", matchedKey)
	}

	// 反向：含 refresh_token 的导入条目无法解析出 user id 时，仍应通过
	// account 键命中已有账号，保持常规导入去重行为。
	full := Record{
		ID: 21,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-2",
			"chatgpt_user_id":    "user-9",
			"access_token":       "token-old",
			"refresh_token":      "refresh-old",
		},
	}
	index = BuildCodexAccountIndex([]Record{full})

	keys = BuildCodexImportIdentityKeys("team-2", "", "", "token-opaque", "refresh-new")
	got, _ = index.Find(keys, "")
	if got == nil || got.ID != full.ID {
		t.Fatalf("Find by account key without entry user id = %v, want account ID %d", got, full.ID)
	}
}

func TestCodexAccountIndexAccessTokenOnlyUsesTokenFingerprint(t *testing.T) {
	existing := Record{
		ID: 22,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       "token-old",
		},
	}
	index := BuildCodexAccountIndex([]Record{existing})

	keys := BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-new", "")
	if got, matchedKey := index.Find(keys, "user-1"); got != nil {
		t.Fatalf("accessToken-only import matched by %q despite different token: account ID %d", matchedKey, got.ID)
	}

	keys = BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-old", "")
	got, matchedKey := index.Find(keys, "user-1")
	if got == nil || got.ID != existing.ID {
		t.Fatalf("Find accessToken-only duplicate by fingerprint = %v, want account ID %d", got, existing.ID)
	}
	if !strings.HasPrefix(matchedKey, "access:") {
		t.Fatalf("matched key = %q, want access fingerprint", matchedKey)
	}
}

func TestCodexAccountIndexKeepsAllCandidatesForSharedAccountKey(t *testing.T) {
	legacy := Record{
		ID: 30,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"access_token":       "token-legacy",
			"refresh_token":      "refresh-legacy",
		},
	}
	member := Record{
		ID: 31,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"chatgpt_user_id":    "user-2",
			"access_token":       "token-member",
			"refresh_token":      "refresh-member",
		},
	}

	// 无论索引构建顺序如何，携带新 user id 的条目都应跳过 user-2 的账号，
	// 命中缺少 user id 的存量账号，而不是因单一候选被遮蔽而落空。
	for _, accounts := range [][]Record{
		{member, legacy},
		{legacy, member},
	} {
		index := BuildCodexAccountIndex(accounts)

		keys := BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-new", "refresh-new")
		got, matchedKey := index.Find(keys, "user-1")
		if got == nil || got.ID != legacy.ID {
			t.Fatalf("Find with shared account key = %v, want legacy account ID %d", got, legacy.ID)
		}
		if matchedKey != "account:team-1" {
			t.Fatalf("matched key = %q, want account:team-1", matchedKey)
		}

		keys = BuildCodexImportIdentityKeys("team-1", "user-2", "", "token-new", "refresh-new")
		got, matchedKey = index.Find(keys, "user-2")
		if got == nil || got.ID != member.ID {
			t.Fatalf("Find by user key = %v, want member account ID %d", got, member.ID)
		}
		if matchedKey != "user:user-2" {
			t.Fatalf("matched key = %q, want user:user-2", matchedKey)
		}
	}
}

func TestCodexAccountIndexUpsertReplacesSameAccount(t *testing.T) {
	legacy := Record{
		ID: 40,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"access_token":       "token-old",
		},
	}
	index := BuildCodexAccountIndex([]Record{legacy})

	backfilled := Record{
		ID: 40,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       "token-new",
			"refresh_token":      "refresh-new",
		},
	}
	index.Add(backfilled)

	// 回填后同一账号在 account 键下应被原位替换而非残留旧副本：
	// 其他成员的条目不应再通过旧副本（无 user id）命中该账号。
	keys := BuildCodexImportIdentityKeys("team-1", "user-2", "", "token-other", "refresh-other")
	if got, matchedKey := index.Find(keys, "user-2"); got != nil {
		t.Fatalf("stale candidate matched after upsert by %q: account ID %d", matchedKey, got.ID)
	}

	keys = BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-other", "refresh-other")
	got, _ := index.Find(keys, "user-1")
	if got == nil || got.ID != backfilled.ID {
		t.Fatalf("Find after upsert = %v, want account ID %d", got, backfilled.ID)
	}
	if uid := CodexCredentialString(got.Credentials, "chatgpt_user_id"); uid != "user-1" {
		t.Fatalf("upsert did not replace credentials, chatgpt_user_id = %q", uid)
	}
}

func TestCodexAccountIndexUpdateRemovesAllPreviousKeys(t *testing.T) {
	legacy := Record{
		ID: 50,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-old",
			"chatgpt_user_id":    "user-old",
			"email":              "old@example.com",
			"access_token":       "access-old",
			"agent_runtime_id":   "runtime-old",
		},
	}
	index := BuildCodexAccountIndex([]Record{legacy})

	updated := Record{
		ID: 50,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-new",
			"chatgpt_user_id":    "user-new",
			"email":              "new@example.com",
			"access_token":       "access-new",
			"agent_runtime_id":   "runtime-new",
		},
	}
	index.Add(updated)

	oldKeys := append(BuildCodexStoredIdentityKeys("team-old", "user-old", "old@example.com", "access-old"), "agent:runtime-old")
	for _, key := range oldKeys {
		if got, matchedKey := index.Find([]string{key}, "user-old"); got != nil {
			t.Fatalf("stale account matched by %q: account ID %d", matchedKey, got.ID)
		}
	}

	newKeys := append(BuildCodexStoredIdentityKeys("team-new", "user-new", "new@example.com", "access-new"), "agent:runtime-new")
	for _, key := range newKeys {
		got, matchedKey := index.Find([]string{key}, "user-new")
		if got == nil || got.ID != updated.ID {
			t.Fatalf("updated account not found by %q: account=%v matched=%q", key, got, matchedKey)
		}
	}
}

func TestCodexAccountIndexUpdatePreservesSharedKeyCandidateOrder(t *testing.T) {
	first := Record{
		ID: 60,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-shared",
			"access_token":       "access-first-old",
		},
	}
	second := Record{
		ID: 61,
		Credentials: map[string]any{
			"chatgpt_account_id": "team-shared",
			"access_token":       "access-second",
		},
	}
	index := BuildCodexAccountIndex([]Record{first, second})

	first.Credentials["access_token"] = "access-first-new"
	index.Add(first)

	got, matchedKey := index.Find([]string{"account:team-shared"}, "")
	if got == nil || got.ID != first.ID {
		t.Fatalf("shared key candidate order changed after update: account=%v matched=%q", got, matchedKey)
	}
}

func TestCodexIdentitySeenDistinguishesTeamMembers(t *testing.T) {
	seen := map[string]CodexSeenIdentity{}
	member1 := BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-1", "refresh-1")
	MarkCodexIdentitySeen(seen, member1, 1, "user-1")

	member2 := BuildCodexImportIdentityKeys("team-1", "user-2", "", "token-2", "refresh-2")
	if index, ok := FirstSeenCodexIdentity(seen, member2, "user-2"); ok {
		t.Fatalf("different team member treated as duplicate of entry %d", index)
	}

	again := BuildCodexImportIdentityKeys("team-1", "user-1", "", "token-3", "refresh-3")
	index, ok := FirstSeenCodexIdentity(seen, again, "user-1")
	if !ok || index != 1 {
		t.Fatalf("same user re-entry dedup = (%d, %v), want (1, true)", index, ok)
	}

	// 无 user id 的条目不应因共享 account id 与已见团队成员互相去重；
	// 只有相同 access token 指纹才视为重复。
	opaque := BuildCodexImportIdentityKeys("team-1", "", "", "token-4", "")
	index, ok = FirstSeenCodexIdentity(seen, opaque, "")
	if ok {
		t.Fatalf("entry without user id dedup = (%d, %v), want no match", index, ok)
	}
}

func TestNormalizeCodexImportUsesJWTSubForAccessTokenOnlyIdentity(t *testing.T) {
	accessToken := buildOriginalCodexTestJWT(t, time.Now().Add(time.Hour), map[string]any{
		"sub": "user-from-access-token",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "workspace-1",
		},
	})

	item, err := NormalizeCodexImportEntry(CodexImportEntry{Index: 1, Value: accessToken}, CodexImportOptions{Now: time.Now, OAuthClientID: "app_EMoamEEZ73f0CkXaXp7hrann"})
	if err != nil {
		t.Fatalf("NormalizeCodexImportEntry error = %v", err)
	}
	if item.UserID != "user-from-access-token" {
		t.Fatalf("UserID = %q, want JWT sub", item.UserID)
	}
	if len(item.IdentityKeys) != 1 || !strings.HasPrefix(item.IdentityKeys[0], "access:") {
		t.Fatalf("IdentityKeys = %v, want access fingerprint only for accessToken-only import", item.IdentityKeys)
	}
	if got := item.Credentials["chatgpt_user_id"]; got != "user-from-access-token" {
		t.Fatalf("credential chatgpt_user_id = %v, want JWT sub", got)
	}
}

func buildOriginalCodexTestJWT(t *testing.T, exp time.Time, extraClaims map[string]any) string {
	t.Helper()
	header := map[string]any{
		"alg": "none",
		"typ": "JWT",
	}
	claims := map[string]any{
		"sub": "user-from-sub",
		"exp": exp.Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range extraClaims {
		claims[k] = v
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(headerBytes) + "." + base64.RawURLEncoding.EncodeToString(claimBytes) + "."
}
