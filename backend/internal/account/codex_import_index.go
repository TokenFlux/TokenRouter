// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
)

type CodexAccountIndex struct {
	accountsByKey   map[string][]Record
	keysByAccountID map[int64]map[string]struct{}
}

// BuildCodexStoredIdentityKeys 生成存量账号索引键，保留 user/account 维度，
// 让 accessToken-only 账号后续升级为完整 OAuth 时仍能命中并更新原账号。
func BuildCodexStoredIdentityKeys(accountID, userID, email, accessToken string) []string {
	keys := make([]string, 0, 3)
	accountID = strings.TrimSpace(accountID)
	userID = strings.TrimSpace(userID)
	accessToken = strings.TrimSpace(accessToken)
	if userID != "" {
		keys = append(keys, "user:"+userID)
	}
	if accountID == "" && userID == "" {
		if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
			keys = append(keys, "email:"+email)
		}
	}
	if accessToken != "" {
		keys = append(keys, "access:"+CodexTokenFingerprint(accessToken))
	}
	if accountID != "" {
		keys = append(keys, "account:"+accountID)
	}
	return keys
}

func BuildCodexAccountIndex(accounts []Record) *CodexAccountIndex {
	index := &CodexAccountIndex{
		accountsByKey:   map[string][]Record{},
		keysByAccountID: map[int64]map[string]struct{}{},
	}
	for _, account := range accounts {
		index.Add(account)
	}
	return index
}

func (i *CodexAccountIndex) Add(account Record) {
	if i == nil {
		return
	}
	// 索引不借用调用者的可变账号图。
	account = *CloneRecord(&account)
	if i.accountsByKey == nil {
		i.accountsByKey = map[string][]Record{}
	}
	if i.keysByAccountID == nil {
		i.keysByAccountID = map[int64]map[string]struct{}{}
	}
	keys := BuildCodexStoredIdentityKeys(
		CodexCredentialString(account.Credentials, "chatgpt_account_id"),
		CodexCredentialString(account.Credentials, "chatgpt_user_id"),
		CodexCredentialString(account.Credentials, "email"),
		CodexCredentialString(account.Credentials, "access_token"),
	)
	orderedKeys := make([]string, 0, len(keys)+1)
	accountKeys := make(map[string]struct{}, len(keys)+1)
	for _, key := range keys {
		if _, exists := accountKeys[key]; exists {
			continue
		}
		accountKeys[key] = struct{}{}
		orderedKeys = append(orderedKeys, key)
	}
	if runtimeID := CodexCredentialString(account.Credentials, "agent_runtime_id"); runtimeID != "" {
		key := "agent:" + runtimeID
		if _, exists := accountKeys[key]; !exists {
			accountKeys[key] = struct{}{}
			orderedKeys = append(orderedKeys, key)
		}
	}

	previousKeys := i.keysByAccountID[account.ID]
	for key := range previousKeys {
		if _, retained := accountKeys[key]; retained {
			i.accountsByKey[key] = upsertCodexAccount(i.accountsByKey[key], account)
			continue
		}
		i.removeFromKey(key, account.ID)
	}
	for _, key := range orderedKeys {
		if _, retained := previousKeys[key]; retained {
			continue
		}
		i.accountsByKey[key] = append(i.accountsByKey[key], account)
	}

	if len(accountKeys) > 0 {
		i.keysByAccountID[account.ID] = accountKeys
		return
	}
	delete(i.keysByAccountID, account.ID)
}

func (i *CodexAccountIndex) removeFromKey(key string, accountID int64) {
	accounts := i.accountsByKey[key]
	kept := accounts[:0]
	for _, account := range accounts {
		if account.ID != accountID {
			kept = append(kept, account)
		}
	}
	if len(kept) == 0 {
		delete(i.accountsByKey, key)
		return
	}
	i.accountsByKey[key] = kept
}

// upsertCodexAccount 保留共享键的全部候选账号，并原位替换已有账号，
// 使存在歧义的旧身份匹配保持原候选顺序。
func upsertCodexAccount(accounts []Record, account Record) []Record {
	for idx := range accounts {
		if accounts[idx].ID == account.ID {
			accounts[idx] = account
			return accounts
		}
	}
	return append(accounts, account)
}

// Find 返回第一个通过跨用户校验的候选账号及其命中的匹配键。
func (i *CodexAccountIndex) Find(keys []string, userID string) (*Record, string) {
	if i == nil {
		return nil, ""
	}
	for _, key := range keys {
		for _, account := range i.accountsByKey[key] {
			if codexIdentityConflicts(key, userID, CodexCredentialString(account.Credentials, "chatgpt_user_id")) {
				continue
			}
			return CloneRecord(&account), key
		}
	}
	return nil, ""
}

// codexIdentityConflicts 判断 account: 键的命中是否把同一 ChatGPT 团队的两个
// 不同成员误连到一起：双方都携带 user id 且不相等时视为冲突。存量索引侧
// 仍保留 account 键，任一侧缺少 user id 时允许匹配，使含 refresh_token
// 的常规导入和 accessToken-only 账号升级为完整 OAuth 时仍能更新原账号。
func codexIdentityConflicts(key, userID, storedUserID string) bool {
	if !strings.HasPrefix(key, "account:") {
		return false
	}
	userID = strings.TrimSpace(userID)
	storedUserID = strings.TrimSpace(storedUserID)
	return userID != "" && storedUserID != "" && userID != storedUserID
}

type CodexSeenIdentity struct {
	index  int
	userID string
}

func FirstSeenCodexIdentity(seen map[string]CodexSeenIdentity, keys []string, userID string) (int, bool) {
	for _, key := range keys {
		entry, ok := seen[key]
		if !ok {
			continue
		}
		if codexIdentityConflicts(key, userID, entry.userID) {
			continue
		}
		return entry.index, true
	}
	return 0, false
}

func MarkCodexIdentitySeen(seen map[string]CodexSeenIdentity, keys []string, index int, userID string) {
	for _, key := range keys {
		seen[key] = CodexSeenIdentity{index: index, userID: userID}
	}
}
