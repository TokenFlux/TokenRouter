// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"time"
)

type legacyKeyRepository struct{ Source APIKeyRepository }

func (p legacyKeyRepository) Create(ctx context.Context, key *apikey.APIKey) error {
	legacy := APIKeyFromView(key)
	err := p.Source.Create(ctx, legacy)
	if legacy != nil && key != nil {
		*key = *APIKeyView(legacy)
	}
	return err
}
func (p legacyKeyRepository) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	v, e := p.Source.GetByID(ctx, id)
	return APIKeyView(v), e
}
func (p legacyKeyRepository) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	return p.Source.GetKeyAndOwnerID(ctx, id)
}
func (p legacyKeyRepository) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	v, e := p.Source.GetByKey(ctx, key)
	return APIKeyView(v), e
}
func (p legacyKeyRepository) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	v, e := p.Source.GetByKeyForAuth(ctx, key)
	return APIKeyView(v), e
}
func (p legacyKeyRepository) Update(ctx context.Context, key *apikey.APIKey, fields APIKeyUpdateFields) error {
	legacy := APIKeyFromView(key)
	err := p.Source.Update(ctx, legacy, fields)
	if legacy != nil && key != nil {
		*key = *APIKeyView(legacy)
	}
	return err
}
func (p legacyKeyRepository) Delete(ctx context.Context, id int64) error {
	return p.Source.Delete(ctx, id)
}
func (p legacyKeyRepository) DeleteWithAudit(ctx context.Context, id int64) error {
	return p.Source.DeleteWithAudit(ctx, id)
}
func (p legacyKeyRepository) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	v, page, e := p.Source.ListByUserID(ctx, userID, params, filters)
	if v == nil {
		return nil, page, e
	}
	out := make([]apikey.APIKey, len(v))
	for i := range v {
		out[i] = *APIKeyView(&v[i])
	}
	return out, page, e
}
func (p legacyKeyRepository) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return p.Source.VerifyOwnership(ctx, userID, apiKeyIDs)
}
func (p legacyKeyRepository) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	return p.Source.CountByUserID(ctx, userID)
}
func (p legacyKeyRepository) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return p.Source.ExistsByKey(ctx, key)
}
func (p legacyKeyRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	v, page, e := p.Source.ListByGroupID(ctx, groupID, params)
	if v == nil {
		return nil, page, e
	}
	out := make([]apikey.APIKey, len(v))
	for i := range v {
		out[i] = *APIKeyView(&v[i])
	}
	return out, page, e
}
func (p legacyKeyRepository) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	v, e := p.Source.SearchAPIKeys(ctx, userID, keyword, limit)
	if v == nil {
		return nil, e
	}
	out := make([]apikey.APIKey, len(v))
	for i := range v {
		out[i] = *APIKeyView(&v[i])
	}
	return out, e
}
func (p legacyKeyRepository) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return p.Source.ClearGroupIDByGroupID(ctx, groupID)
}
func (p legacyKeyRepository) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	return p.Source.UpdateGroupIDByUserAndGroup(ctx, userID, oldGroupID, newGroupID)
}
func (p legacyKeyRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return p.Source.CountByGroupID(ctx, groupID)
}
func (p legacyKeyRepository) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return p.Source.ListKeysByUserID(ctx, userID)
}
func (p legacyKeyRepository) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return p.Source.ListKeysByGroupID(ctx, groupID)
}
func (p legacyKeyRepository) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return p.Source.IncrementQuotaUsed(ctx, id, amount)
}
func (p legacyKeyRepository) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	return p.Source.UpdateLastUsed(ctx, id, usedAt)
}
func (p legacyKeyRepository) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return p.Source.IncrementRateLimitUsage(ctx, id, cost)
}
func (p legacyKeyRepository) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return p.Source.ResetRateLimitWindows(ctx, id)
}
func (p legacyKeyRepository) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	return p.Source.GetRateLimitData(ctx, id)
}

type keyAllRows interface {
	ListAllByUserID(context.Context, int64, APIKeyListFilters) ([]APIKey, error)
}
type keyQuotaState interface {
	IncrementQuotaUsedAndGetState(context.Context, int64, float64) (*APIKeyQuotaUsageState, error)
}
type legacyKeyAll struct {
	legacyKeyRepository
	all keyAllRows
}

func (p legacyKeyAll) ListAllByUserID(ctx context.Context, id int64, filters apikey.APIKeyListFilters) ([]apikey.APIKey, error) {
	v, e := p.all.ListAllByUserID(ctx, id, filters)
	if v == nil {
		return nil, e
	}
	out := make([]apikey.APIKey, len(v))
	for i := range v {
		out[i] = *APIKeyView(&v[i])
	}
	return out, e
}

type legacyKeyQuota struct {
	legacyKeyRepository
	quota keyQuotaState
}

func (p legacyKeyQuota) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, cost float64) (*apikey.APIKeyQuotaUsageState, error) {
	return p.quota.IncrementQuotaUsedAndGetState(ctx, id, cost)
}

type legacyKeyAllQuota struct {
	legacyKeyAll
	quota keyQuotaState
}

func (p legacyKeyAllQuota) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, cost float64) (*apikey.APIKeyQuotaUsageState, error) {
	return p.quota.IncrementQuotaUsedAndGetState(ctx, id, cost)
}
func KeyRepositoryView(source APIKeyRepository) apikey.APIKeyRepository {
	if source == nil {
		return nil
	}
	if direct, ok := source.(interface {
		APIKeyRepository() apikey.APIKeyRepository
	}); ok {
		return direct.APIKeyRepository()
	}
	base := legacyKeyRepository{Source: source}
	all, hasAll := source.(keyAllRows)
	quota, hasQuota := source.(keyQuotaState)
	if hasAll && hasQuota {
		return legacyKeyAllQuota{legacyKeyAll{base, all}, quota}
	}
	if hasAll {
		return legacyKeyAll{base, all}
	}
	if hasQuota {
		return legacyKeyQuota{base, quota}
	}
	return base
}
