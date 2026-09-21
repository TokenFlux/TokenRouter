package account

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// WithManagedRefresh 在同一刷新锁和拥有者内执行显式管理流程；锁内重新读取，保留原独立提交顺序。
func (api *OAuthRefreshAPI) WithManagedRefresh(ctx context.Context, observed *Record, key string, apply func(context.Context, *Record) (*Record, string, error)) (*Record, string, error) {
	if err := ValidateManagedRefreshTarget(observed); err != nil {
		return nil, "", err
	}
	if api == nil || api.accountRepo == nil {
		return nil, "", errors.New("oauth refresh account repository is not configured")
	}
	if observed == nil {
		return nil, "", ErrAccountNotFound
	}
	ctx, finish, err := api.beginRefresh(ctx)
	if err != nil {
		return nil, "", err
	}
	defer finish()
	release, held, err := api.acquireRefreshLock(ctx, observed.ID, key)
	if err != nil {
		return nil, "", err
	}
	if !held {
		defer release()
	}
	current, err := api.accountRepo.GetByID(ctx, observed.ID)
	if err != nil {
		return nil, "", err
	}
	if current == nil || current.ID != observed.ID || current.Platform != observed.Platform || current.Type != observed.Type {
		return nil, "", ErrRefreshAccountStateChanged
	}
	if err := ValidateManagedRefreshTarget(current); err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if held {
		return CloneRecord(current), "", nil
	}
	return apply(ctx, CloneRecord(current))
}

// ValidateManagedRefreshTarget 保持管理刷新在交换前拒绝非 OAuth 和凭据影子的原错误。
func ValidateManagedRefreshTarget(value *Record) error {
	if value == nil {
		return ErrAccountNotFound
	}
	if !value.IsOAuth() && !value.IsQoderCosy() {
		return apperror.BadRequest("NOT_OAUTH", "cannot refresh non-OAuth account")
	}
	if value.IsCredentialShadow() {
		return apperror.BadRequest("SPARK_SHADOW_NO_REFRESH", "cannot refresh spark shadow account; its credentials are managed by the parent account")
	}
	return nil
}
