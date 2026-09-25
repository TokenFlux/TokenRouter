// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"golang.org/x/sync/errgroup"
)

// GoogleOneTierObservation 只保留本次供应商查询的观测，不能携带旧配置整图。
type GoogleOneTierObservation struct {
	TierID     string
	Storage    *GoogleOneStorage
	ObservedAt time.Time
}
type GoogleOneStorage struct{ Limit, Usage int64 }
type TierManagementStore interface {
	GetAccount(context.Context, int64) (*Record, error)
	GetAccountsByIDs(context.Context, []int64) ([]*Record, error)
	ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]Record, int64, error)
	UpdateAccount(context.Context, int64, *UpdateAccountInput) (*Record, error)
}
type TierManagementOptions struct {
	Observe func(context.Context, *Record) (GoogleOneTierObservation, error)
}
type TierRefreshResult struct {
	TierID      string
	StorageInfo map[string]any
}
type TierRefreshFailure struct {
	AccountID int64
	Error     string
}
type TierBatchResult struct {
	Total, Success, Failed int
	Errors                 []TierRefreshFailure
}

// TierManagement 拥有资格、批量查询和条件写入，实际 Drive 请求由平台端口执行。
type TierManagement struct {
	store    TierManagementStore
	options  TierManagementOptions
	activity operationActivity
}

func NewTierManagement(store TierManagementStore, options TierManagementOptions) *TierManagement {
	return &TierManagement{store: store, options: options}
}
func (s *TierManagement) StopContext(ctx context.Context) error {
	return s.activity.stop(ctx, "account tier maintenance")
}
func (s *TierManagement) Refresh(ctx context.Context, v *Record) (*TierRefreshResult, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrRefreshStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.refresh(ctx, v)
}
func (s *TierManagement) refresh(ctx context.Context, v *Record) (*TierRefreshResult, error) {
	// 停止或调用方取消后，排队项不能再开始供应商观测。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v.Platform != PlatformGemini || v.Type != AccountTypeOAuth {
		return nil, apperror.BadRequest("", "Only Gemini OAuth accounts support tier refresh")
	}
	kind, _ := v.Credentials["oauth_type"].(string)
	if kind != "google_one" {
		return nil, apperror.BadRequest("", "Only google_one OAuth accounts support tier refresh")
	}
	expected := FailureVersion(v).CredentialVersion
	observation, err := s.options.Observe(ctx, CloneRecord(v))
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	extra, credentials := ProjectGoogleOneTier(v, observation)
	patch := GoogleOneTierExtraPatch(observation)
	_, err = s.store.UpdateAccount(ctx, v.ID, &UpdateAccountInput{ExpectedCredentials: &expected, Credentials: map[string]any{"tier_id": credentials["tier_id"]}, PatchCredentials: true, Extra: patch, PatchExtra: true})
	if err != nil {
		return nil, err
	}
	return &TierRefreshResult{TierID: observation.TierID, StorageInfo: extra}, nil
}

// GoogleOneTierExtraPatch 只写本轮 Drive 观测字段。
func GoogleOneTierExtraPatch(observation GoogleOneTierObservation) map[string]any {
	out := map[string]any{}
	if observation.Storage != nil {
		out["drive_storage_limit"] = observation.Storage.Limit
		out["drive_storage_usage"] = observation.Storage.Usage
		out["drive_tier_updated_at"] = observation.ObservedAt.Format(time.RFC3339)
	}
	return out
}

// ProjectGoogleOneTier 保留原 tier 响应和旧消费者的完整返回形状；写入使用明确字段增量。
func ProjectGoogleOneTier(v *Record, observation GoogleOneTierObservation) (map[string]any, map[string]any) {
	extra := CloneValues(v.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	for k, x := range GoogleOneTierExtraPatch(observation) {
		extra[k] = x
	}
	credentials := CloneValues(v.Credentials)
	if credentials == nil {
		credentials = map[string]any{}
	}
	credentials["tier_id"] = observation.TierID
	return extra, credentials
}
func (s *TierManagement) Batch(ctx context.Context, ids []int64) (*TierBatchResult, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrRefreshStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	accounts := make([]*Record, 0)
	if len(ids) == 0 {
		all, _, err := s.store.ListAccounts(ctx, 1, 10000, "gemini", "oauth", "", "", 0, "", "name", "asc")
		if err != nil {
			return nil, err
		}
		for i := range all {
			v := &all[i]
			kind, _ := v.Credentials["oauth_type"].(string)
			if kind == "google_one" {
				accounts = append(accounts, v)
			}
		}
	} else {
		fetched, err := s.store.GetAccountsByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, v := range fetched {
			if v == nil || v.Platform != PlatformGemini || v.Type != AccountTypeOAuth {
				continue
			}
			kind, _ := v.Credentials["oauth_type"].(string)
			if kind == "google_one" {
				accounts = append(accounts, v)
			}
		}
	}
	result := &TierBatchResult{Total: len(accounts)}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10)
	for _, v := range accounts {
		g.Go(func() error {
			_, err := s.refresh(gctx, v)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, TierRefreshFailure{AccountID: v.ID, Error: err.Error()})
			} else {
				result.Success++
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}
