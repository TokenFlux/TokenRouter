// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"log/slog"
	"time"
)

// legacyAccountAdminStore 仅为旧独立构造入口提供值投影；完整应用绑定新 AccountStore。
type legacyAccountAdminStore struct{ AccountRepository }

func (r legacyAccountAdminStore) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.AccountRepository.GetByID(ctx, id)
	return AccountRecordView(v), err
}
func (r legacyAccountAdminStore) GetByIDs(ctx context.Context, ids []int64) ([]*acctcore.Record, error) {
	v, err := r.AccountRepository.GetByIDs(ctx, ids)
	return accountRecordPointers(v), err
}
func (r legacyAccountAdminStore) ListShadowsByParent(ctx context.Context, id int64) ([]*acctcore.Record, error) {
	v, err := r.AccountRepository.ListShadowsByParent(ctx, id)
	return accountRecordPointers(v), err
}
func accountRecordPointers(values []*Account) []*acctcore.Record {
	if values == nil {
		return nil
	}
	out := make([]*acctcore.Record, len(values))
	for i, v := range values {
		out[i] = AccountRecordView(v)
	}
	return out
}
func (r legacyAccountAdminStore) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, kind, status, search string, group int64, privacy string) ([]acctcore.Record, *pagination.PaginationResult, error) {
	v, p, err := r.AccountRepository.ListWithFilters(ctx, params, platform, kind, status, search, group, privacy)
	return AccountRecordsView(v), p, err
}
func (r legacyAccountAdminStore) ListAllWithFilters(ctx context.Context, platform, kind, status, search string, group int64, privacy string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListAllWithFilters(ctx, platform, kind, status, search, group, privacy)
	return AccountRecordsView(v), err
}
func (r legacyAccountAdminStore) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, group int64, platforms []string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListSchedulableByGroupIDAndPlatforms(ctx, group, platforms)
	return AccountRecordsView(v), err
}
func (r legacyAccountAdminStore) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListSchedulableUngroupedByPlatforms(ctx, platforms)
	return AccountRecordsView(v), err
}
func (r legacyAccountAdminStore) ListSchedulableByGroupIDAndPlatform(ctx context.Context, group int64, platform string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListSchedulableByGroupIDAndPlatform(ctx, group, platform)
	return AccountRecordsView(v), err
}
func (r legacyAccountAdminStore) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListSchedulableUngroupedByPlatform(ctx, platform)
	return AccountRecordsView(v), err
}
func (s *adminServiceImpl) accountAdministration() *acctcore.Admin {
	if s == nil {
		return nil
	}
	if s.accountAdmin != nil {
		return s.accountAdmin
	}
	var store acctcore.AdminStore
	if s.accountRepo != nil {
		store = legacyAccountAdminStore{s.accountRepo}
	}
	var groups acctcore.AdminGroups
	if s.groupRepo != nil {
		groups = legacyAccountAdminGroups{s.groupRepo}
	}
	var duplicates acctcore.DuplicateStore
	if s.accountDuplicateRepo != nil {
		duplicates = legacyAccountDuplicateStore{s.accountDuplicateRepo}
	}
	return acctcore.NewAdmin(store, acctcore.AdminOptions{ShadowModels: defaultSparkShadowModelMapping, Duplicates: duplicates, Groups: groups, Proxies: s.proxyRepo, Creation: acctcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: newCodexFingerprintSeed}, Credentials: LegacyCreateCredentialHooks(s.httpUpstream, s.tlsFPProfileService), Background: RunBackgroundTask, Error: slog.Error, Privacy: legacyAccountPrivacy(s.accountRepo, s.proxyRepo, s.privacyClientFactory), Quotas: s.accountRepo, RuntimeBlocker: s.runtimeBlocker})
}

func (r legacyAccountAdminStore) Create(ctx context.Context, value *acctcore.Record) error {
	v := AccountFromRecord(value)
	err := r.AccountRepository.Create(ctx, v)
	if value != nil && v != nil {
		*value = *AccountRecordView(v)
	}
	return err
}
func (r legacyAccountAdminStore) ListByGroup(ctx context.Context, id int64) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListByGroup(ctx, id)
	return AccountRecordsView(v), err
}

type legacyAccountAdminGroups struct{ source GroupRepository }

func (g legacyAccountAdminGroups) DefaultGroup(ctx context.Context, platform string) (*acctcore.GroupReference, error) {
	v, err := findPlatformDefaultGroup(ctx, g.source, platform)
	if v == nil {
		return nil, err
	}
	return &acctcore.GroupReference{ID: v.ID, Name: v.Name, Platform: v.Platform, RequireOAuthOnly: v.RequireOAuthOnly}, err
}
func (g legacyAccountAdminGroups) GetGroup(ctx context.Context, id int64) (*acctcore.GroupReference, error) {
	v, err := g.source.GetByID(ctx, id)
	if v == nil {
		return nil, err
	}
	return &acctcore.GroupReference{ID: v.ID, Name: v.Name, Platform: v.Platform, RequireOAuthOnly: v.RequireOAuthOnly}, err
}

func (r legacyAccountAdminStore) FindByExtraField(ctx context.Context, key string, value any) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.FindByExtraField(ctx, key, value)
	return AccountRecordsView(v), err
}

type legacyAccountDuplicateStore struct{ source AccountDuplicateRepository }

func (s legacyAccountDuplicateStore) CreateWithAccountGroups(ctx context.Context, value *acctcore.Record, groups []acctcore.GroupMembership) error {
	v := AccountFromRecord(value)
	members := make([]AccountGroup, len(groups))
	for i, g := range groups {
		members[i] = AccountGroup{AccountID: g.AccountID, GroupID: g.GroupID, CreatedAt: g.CreatedAt}
	}
	err := s.source.CreateWithAccountGroups(ctx, v, members)
	if v != nil && value != nil {
		*value = *AccountRecordView(v)
	}
	for i := range groups {
		groups[i].AccountID = members[i].AccountID
		groups[i].CreatedAt = members[i].CreatedAt
	}
	return err
}

func (r legacyAccountAdminStore) Update(ctx context.Context, value *acctcore.Record) error {
	v := AccountFromRecord(value)
	err := r.AccountRepository.Update(ctx, v)
	if value != nil && v != nil {
		*value = *AccountRecordView(v)
	}
	return err
}
func (g legacyAccountAdminGroups) ActiveGroups(ctx context.Context, platform string) ([]acctcore.GroupReference, error) {
	rows, err := g.source.ListActiveByPlatform(ctx, platform)
	if rows == nil {
		return nil, err
	}
	out := make([]acctcore.GroupReference, len(rows))
	for i, v := range rows {
		out[i] = acctcore.GroupReference{ID: v.ID, Name: v.Name, Platform: v.Platform, RequireOAuthOnly: v.RequireOAuthOnly}
	}
	return out, err
}

type legacyGroupExistenceLookup struct{ source GroupRepository }

func (g legacyGroupExistenceLookup) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	v, err := g.source.GetByID(ctx, id)
	return RoutingGroupView(v), err
}
func (g legacyAccountAdminGroups) ValidateGroups(ctx context.Context, ids []int64) error {
	if g.source == nil {
		return routing.ValidateGroupIDs(ctx, nil, ids)
	}
	var lookup routing.GroupExistenceLookup = legacyGroupExistenceLookup(g)
	if batch, ok := g.source.(routing.GroupExistenceBatchReader); ok {
		lookup = struct {
			routing.GroupExistenceLookup
			routing.GroupExistenceBatchReader
		}{lookup, batch}
	}
	return routing.ValidateGroupIDs(ctx, lookup, ids)
}
