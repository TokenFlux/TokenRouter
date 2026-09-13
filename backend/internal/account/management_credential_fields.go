package account

import (
	"context"
	"fmt"
)

// ManagementAccountMissing 保留批量预验证阶段的 404 展示，原始原因供内部错误链读取。
type ManagementAccountMissing struct {
	ID    int64
	Cause error
}

func (e *ManagementAccountMissing) Error() string { return fmt.Sprintf("Account %d not found", e.ID) }
func (e *ManagementAccountMissing) Unwrap() error { return e.Cause }

type ManagementPatchItem struct {
	AccountID int64
	Success   bool
	Error     string
}
type ManagementPatchResult struct {
	Success, Failed       int
	SuccessIDs, FailedIDs []int64
	Results               []ManagementPatchItem
}

// ValidateCredentialFieldValue 只允许原管理接口的三个字段和既有值类型。
func ValidateCredentialFieldValue(field string, value any) error {
	switch field {
	case "intercept_warmup_requests":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("intercept_warmup_requests must be boolean")
		}
	case "account_uuid", "org_uuid":
		if value != nil {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be string or null", field)
			}
		}
	default:
		return fmt.Errorf("unsupported credential field: %s", field)
	}
	return nil
}

// PatchCredentials 保留先完整预验证、再逐项提交；只发送明确字段，锁内继承其他最新凭据与配置。
func (s *ManagementBatch) PatchCredentials(ctx context.Context, ids []int64, field string, value any) (*ManagementPatchResult, error) {
	if err := ValidateCredentialFieldValue(field, value); err != nil {
		return nil, err
	}
	for _, id := range ids {
		v, err := s.store.GetAccount(ctx, id)
		if err != nil || v == nil {
			return nil, &ManagementAccountMissing{ID: id, Cause: err}
		}
	}
	result := &ManagementPatchResult{SuccessIDs: make([]int64, 0, len(ids)), FailedIDs: make([]int64, 0, len(ids)), Results: make([]ManagementPatchItem, 0, len(ids))}
	for _, id := range ids {
		_, err := s.store.UpdateAccount(ctx, id, &UpdateAccountInput{Credentials: map[string]any{field: value}, PatchCredentials: true})
		if err != nil {
			result.Failed++
			result.FailedIDs = append(result.FailedIDs, id)
			result.Results = append(result.Results, ManagementPatchItem{AccountID: id, Error: err.Error()})
			continue
		}
		result.Success++
		result.SuccessIDs = append(result.SuccessIDs, id)
		result.Results = append(result.Results, ManagementPatchItem{AccountID: id, Success: true})
	}
	return result, nil
}
