// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	strings "strings"
	time "time"
)

func (h *CodexImporter) Import(ctx context.Context, req CodexSessionImportRequest, entries []CodexImportEntry) (CodexSessionImportResult, error) {
	result := CodexSessionImportResult{
		Total: len(entries),
		Items: make([]CodexSessionImportItem, 0, len(entries)),
	}

	existingAccounts, err := h.archive.ListAccountsForImport(ctx, PlatformOpenAI, AccountTypeOAuth, "", "", 0, "", "created_at", "desc")
	if err != nil {
		return result, err
	}
	index := BuildCodexAccountIndex(existingAccounts)

	updateExisting := true
	if req.UpdateExisting != nil {
		updateExisting = *req.UpdateExisting
	}
	concurrency := 3
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := 50
	if req.Priority != nil {
		priority = *req.Priority
	}
	credentialExtras := SanitizeCodexImportCredentialExtras(req.CredentialExtras)
	skipDefaultGroupBind := false
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}
	skipMixedChannelCheck := req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk

	seenIdentity := map[string]CodexSeenIdentity{}
	for _, entry := range entries {
		item, err := NormalizeCodexImportEntry(entry, h.options)
		if err != nil {
			result.Failed++
			result.Items = append(result.Items, CodexSessionImportItem{
				Index:   entry.Index,
				Action:  "failed",
				Message: err.Error(),
			})
			result.Errors = append(result.Errors, CodexSessionImportMessage{
				Index:   entry.Index,
				Message: err.Error(),
			})
			continue
		}
		accountName := buildCodexCreateAccountName(req.Name, item, entry.Index, len(entries))
		effectiveExpiresAt, credentialExpiresAt, autoPauseOnExpired, expiryWarnings, expiryErr := ResolveCodexImportExpiry(req, item, h.options.Now)
		if expiryErr != nil {
			result.Failed++
			result.Items = append(result.Items, CodexSessionImportItem{
				Index:   entry.Index,
				Name:    accountName,
				Action:  "failed",
				Message: expiryErr.Error(),
			})
			result.Errors = append(result.Errors, CodexSessionImportMessage{
				Index:   entry.Index,
				Name:    accountName,
				Message: expiryErr.Error(),
			})
			continue
		}
		item.WarningTexts = append(item.WarningTexts, expiryWarnings...)
		if credentialExpiresAt != nil {
			item.Credentials["expires_at"] = credentialExpiresAt.Format(time.RFC3339)
		}
		credentials := MergeCodexImportMap(item.Credentials, credentialExtras)
		extra := MergeCodexImportMap(req.Extra, item.Extra)
		for _, warning := range item.WarningTexts {
			result.Warnings = append(result.Warnings, CodexSessionImportMessage{
				Index:   entry.Index,
				Name:    accountName,
				Message: warning,
			})
		}

		if duplicateIndex, ok := FirstSeenCodexIdentity(seenIdentity, item.IdentityKeys, item.UserID); ok {
			message := fmt.Sprintf("与第 %d 条导入项重复，已跳过", duplicateIndex)
			result.Skipped++
			result.Items = append(result.Items, CodexSessionImportItem{
				Index:   entry.Index,
				Name:    accountName,
				Action:  "skipped",
				Message: message,
			})
			result.Warnings = append(result.Warnings, CodexSessionImportMessage{
				Index:   entry.Index,
				Name:    accountName,
				Message: message,
			})
			continue
		}
		MarkCodexIdentitySeen(seenIdentity, item.IdentityKeys, entry.Index, item.UserID)

		existing, matchedKey := index.Find(item.IdentityKeys, item.UserID)
		if existing != nil && updateExisting {
			if strings.HasPrefix(matchedKey, "account:") && item.UserID != "" &&
				CodexCredentialString(existing.Credentials, "chatgpt_user_id") == "" {
				result.Warnings = append(result.Warnings, CodexSessionImportMessage{
					Index:   entry.Index,
					Name:    accountName,
					Message: "已有账号未记录 chatgpt_user_id，已按共享的 chatgpt_account_id 匹配并回填，请确认两者属于同一用户",
				})
			}
			preserveExistingRefresh := item.RefreshToken == "" &&
				CodexCredentialString(existing.Credentials, "refresh_token") != ""
			if preserveExistingRefresh {
				result.Warnings = append(result.Warnings, CodexSessionImportMessage{
					Index:   entry.Index,
					Name:    accountName,
					Message: "已有账号包含 refresh_token，本次 accessToken-only 导入已保留自动续期凭据",
				})
				effectiveExpiresAt = nil
				autoPauseOnExpired = nil
			}
			mergedCredentials := MergeCodexImportCredentials(existing.Credentials, credentials, item)
			mergedExtra := MergeCodexImportMap(existing.Extra, extra)
			updateInput := &UpdateAccountInput{
				Credentials:        mergedCredentials,
				Extra:              mergedExtra,
				Concurrency:        req.Concurrency,
				Priority:           req.Priority,
				RateMultiplier:     req.RateMultiplier,
				LoadFactor:         req.LoadFactor,
				ExpiresAt:          effectiveExpiresAt,
				AutoPauseOnExpired: autoPauseOnExpired,
			}
			if req.ProxyID != nil {
				updateInput.ProxyID = req.ProxyID
			}
			if len(req.GroupIDs) > 0 {
				groupIDs := append([]int64(nil), req.GroupIDs...)
				updateInput.GroupIDs = &groupIDs
				updateInput.SkipMixedChannelCheck = skipMixedChannelCheck
			}
			updated, updateErr := h.accounts.UpdateAccount(ctx, existing.ID, updateInput)
			if updateErr != nil {
				result.Failed++
				result.Items = append(result.Items, CodexSessionImportItem{
					Index:   entry.Index,
					Name:    accountName,
					Action:  "failed",
					Message: updateErr.Error(),
				})
				result.Errors = append(result.Errors, CodexSessionImportMessage{
					Index:   entry.Index,
					Name:    accountName,
					Message: updateErr.Error(),
				})
				continue
			}
			if h.options.Invalidate != nil && updated != nil {
				_ = h.options.Invalidate(ctx, updated)
			}
			result.Updated++
			accountID := existing.ID
			if updated != nil {
				accountID = updated.ID
				index.Add(*updated)
			}
			result.Items = append(result.Items, CodexSessionImportItem{
				Index:     entry.Index,
				Name:      accountName,
				Action:    "updated",
				AccountID: accountID,
			})
			continue
		}

		account, createErr := h.accounts.CreateAccount(ctx, &CreateAccountInput{
			Name:                  accountName,
			Notes:                 req.Notes,
			Platform:              PlatformOpenAI,
			Type:                  AccountTypeOAuth,
			Credentials:           credentials,
			Extra:                 extra,
			ProxyID:               req.ProxyID,
			Concurrency:           concurrency,
			Priority:              priority,
			RateMultiplier:        req.RateMultiplier,
			LoadFactor:            req.LoadFactor,
			GroupIDs:              req.GroupIDs,
			ExpiresAt:             effectiveExpiresAt,
			AutoPauseOnExpired:    autoPauseOnExpired,
			SkipDefaultGroupBind:  skipDefaultGroupBind,
			SkipMixedChannelCheck: skipMixedChannelCheck,
		})
		if createErr != nil {
			result.Failed++
			result.Items = append(result.Items, CodexSessionImportItem{
				Index:   entry.Index,
				Name:    accountName,
				Action:  "failed",
				Message: createErr.Error(),
			})
			result.Errors = append(result.Errors, CodexSessionImportMessage{
				Index:   entry.Index,
				Name:    accountName,
				Message: createErr.Error(),
			})
			continue
		}
		if account != nil {
			index.Add(*account)
		}
		result.Created++
		accountID := int64(0)
		if account != nil {
			accountID = account.ID
		}
		result.Items = append(result.Items, CodexSessionImportItem{
			Index:     entry.Index,
			Name:      accountName,
			Action:    "created",
			AccountID: accountID,
		})
	}

	return result, nil
}
