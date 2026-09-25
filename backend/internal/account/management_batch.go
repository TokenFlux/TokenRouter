// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"
)

type ManagementBatchStore interface {
	CreateAccount(context.Context, *CreateAccountInput) (*Record, error)
	GetAccount(context.Context, int64) (*Record, error)
	UpdateAccount(context.Context, int64, *UpdateAccountInput) (*Record, error)
	GetAccountsByIDs(context.Context, []int64) ([]*Record, error)
	DeleteAccount(context.Context, int64) error
}
type ManagementBatchFailure struct {
	AccountID int64
	Error     string
}
type ManagementBatchWarning struct {
	AccountID int64
	Warning   string
}
type ManagementBatchResult struct {
	Total, Success, Failed int
	Errors                 []ManagementBatchFailure
	Warnings               []ManagementBatchWarning
}
type ManagementDeleteResult struct {
	Total, Success, Failed int
	SuccessIDs, FailedIDs  []int64
	Errors                 []ManagementBatchFailure
}

// ManagementBatch 拥有原批量部分成功、缺失账号与受限并发规则，不启动持久后台任务。
type ManagementBatch struct {
	creation ManagementCreationOptions
	store    ManagementBatchStore
	managed  *ManagedRefreshService
}

func NewManagementBatch(store ManagementBatchStore, managed *ManagedRefreshService, creation ...ManagementCreationOptions) *ManagementBatch {
	options := ManagementCreationOptions{}
	if len(creation) > 0 {
		options = creation[0]
	}
	return &ManagementBatch{store: store, managed: managed, creation: options}
}

// DeleteNormalized 接收 HTTP 已归一化的正数 ID，保留父子删除依赖及原五并发。
func (s *ManagementBatch) DeleteNormalized(ctx context.Context, accountIDs []int64) (*ManagementDeleteResult, error) {
	accounts, err := s.store.GetAccountsByIDs(ctx, accountIDs)
	if err != nil {
		return nil, err
	}

	requestedIDs := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		requestedIDs[accountID] = struct{}{}
	}
	accountsByID := make(map[int64]*Record, len(accounts))
	for _, account := range accounts {
		if account != nil {
			accountsByID[account.ID] = account
		}
	}

	rootIDs := make([]int64, 0, len(accountIDs))
	dependentIDs := make(map[int64][]int64)
	failedIDs := make([]int64, 0)
	errorsByAccount := make([]ManagementBatchFailure, 0)
	for _, accountID := range accountIDs {
		account := accountsByID[accountID]
		if account == nil {
			failedIDs = append(failedIDs, accountID)
			errorsByAccount = append(errorsByAccount, ManagementBatchFailure{
				AccountID: accountID,
				Error:     "account not found",
			})
			continue
		}

		rootID := accountID
		visited := map[int64]struct{}{accountID: {}}
		for {
			current := accountsByID[rootID]
			if current == nil || current.ParentAccountID == nil {
				break
			}
			parentID := *current.ParentAccountID
			if _, selected := requestedIDs[parentID]; !selected {
				break
			}
			if _, exists := accountsByID[parentID]; !exists {
				break
			}
			if _, cyclic := visited[parentID]; cyclic {
				rootID = accountID
				break
			}
			visited[parentID] = struct{}{}
			rootID = parentID
		}

		if rootID != accountID {
			dependentIDs[rootID] = append(dependentIDs[rootID], accountID)
			continue
		}
		rootIDs = append(rootIDs, accountID)
	}

	const maxConcurrency = 5
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	var mu sync.Mutex
	successIDs := make([]int64, 0, len(accountIDs))

	// 单个账号失败不取消其它删除任务，失败信息在结果中逐项返回。
	for _, id := range rootIDs {
		accountID := id
		g.Go(func() error {
			err := s.store.DeleteAccount(gctx, accountID)

			mu.Lock()
			defer mu.Unlock()
			affectedIDs := append([]int64{accountID}, dependentIDs[accountID]...)
			if err != nil {
				for _, affectedID := range affectedIDs {
					failedIDs = append(failedIDs, affectedID)
					errorsByAccount = append(errorsByAccount, ManagementBatchFailure{
						AccountID: affectedID,
						Error:     err.Error(),
					})
				}
				return nil
			}
			successIDs = append(successIDs, affectedIDs...)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	sort.Slice(successIDs, func(i, j int) bool { return successIDs[i] < successIDs[j] })
	sort.Slice(failedIDs, func(i, j int) bool { return failedIDs[i] < failedIDs[j] })
	sort.Slice(errorsByAccount, func(i, j int) bool {
		return errorsByAccount[i].AccountID < errorsByAccount[j].AccountID
	})

	return &ManagementDeleteResult{Total: len(accountIDs), Success: len(successIDs), Failed: len(failedIDs), SuccessIDs: successIDs, FailedIDs: failedIDs, Errors: errorsByAccount}, nil
}
func (s *ManagementBatch) Refresh(ctx context.Context, accountIDs []int64) (*ManagementBatchResult, error) {
	accounts, err := s.store.GetAccountsByIDs(ctx, accountIDs)
	if err != nil {
		return nil, err
	}

	// 建立已获取账号的 ID 集合，检测缺失的 ID
	foundIDs := make(map[int64]bool, len(accounts))
	for _, acc := range accounts {
		if acc != nil {
			foundIDs[acc.ID] = true
		}
	}

	const maxConcurrency = 10
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	var mu sync.Mutex
	var successCount, failedCount int
	var errors []ManagementBatchFailure
	var warnings []ManagementBatchWarning

	// 将不存在的账号 ID 标记为失败
	for _, id := range accountIDs {
		if !foundIDs[id] {
			failedCount++
			errors = append(errors, ManagementBatchFailure{
				AccountID: id,
				Error:     "account not found",
			})
		}
	}

	// 注意：所有 goroutine 必须 return nil，避免 errgroup cancel 其他并发任务
	for _, account := range accounts {
		acc := account // 闭包捕获
		if acc == nil {
			continue
		}
		g.Go(func() error {
			_, warning, err := s.managed.Refresh(gctx, acc)
			mu.Lock()
			if err != nil {
				failedCount++
				errors = append(errors, ManagementBatchFailure{
					AccountID: acc.ID,
					Error:     err.Error(),
				})
			} else {
				successCount++
				if warning != "" {
					warnings = append(warnings, ManagementBatchWarning{
						AccountID: acc.ID,
						Warning:   warning,
					})
				}
			}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &ManagementBatchResult{Total: len(accountIDs), Success: successCount, Failed: failedCount, Errors: errors, Warnings: warnings}, nil
}
func (s *ManagementBatch) ClearError(ctx context.Context, accountIDs []int64) (*ManagementBatchResult, error) {
	const maxConcurrency = 10
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	var mu sync.Mutex
	var successCount, failedCount int
	var errors []ManagementBatchFailure

	// 注意：所有 goroutine 必须 return nil，避免 errgroup cancel 其他并发任务
	for _, id := range accountIDs {
		accountID := id // 闭包捕获
		g.Go(func() error {
			_, err := s.managed.ClearError(gctx, accountID)
			if err != nil {
				mu.Lock()
				failedCount++
				errors = append(errors, ManagementBatchFailure{
					AccountID: accountID,
					Error:     err.Error(),
				})
				mu.Unlock()
				return nil
			}

			mu.Lock()
			successCount++
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &ManagementBatchResult{Total: len(accountIDs), Success: successCount, Failed: failedCount, Errors: errors}, nil
}
