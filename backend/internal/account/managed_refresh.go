package account

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type ManagedCredentialStore interface {
	GetAccount(context.Context, int64) (*Record, error)
	UpdateAccountExtra(context.Context, int64, map[string]any) error
	UpdateAccount(context.Context, int64, *UpdateAccountInput) (*Record, error)
	ClearAccountError(context.Context, int64) (*Record, error)
}
type ManagedCredentialPrivacy interface {
	EnsureOpenAIPrivacy(context.Context, *Record) string
	EnsureAntigravityPrivacy(context.Context, *Record) string
}
type ManagedRefreshObservation struct {
	Credentials      map[string]any
	ProjectIDMissing bool
}
type ManagedRefreshOptions struct {
	Store       ManagedCredentialStore
	Privacy     ManagedCredentialPrivacy
	Coordinate  func(context.Context, *Record, string, func(context.Context, *Record) (*Record, string, error)) (*Record, string, error)
	CacheKey    func(*Record) string
	Exchange    func(context.Context, *Record) (ManagedRefreshObservation, error)
	Invalidate  func(context.Context, *Record) error
	Log         func(string, ...any)
	Warn, Error func(string, ...any)
}

// ManagedRefreshService 拥有单账号/批量共用的管理刷新规则，协调器包围原独立提交顺序。
type ManagedRefreshService struct{ options ManagedRefreshOptions }

func NewManagedRefreshService(options ManagedRefreshOptions) *ManagedRefreshService {
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if options.Error == nil {
		options.Error = func(string, ...any) {}
	}
	return &ManagedRefreshService{options: options}
}
func (s *ManagedRefreshService) Refresh(ctx context.Context, value *Record) (*Record, string, error) {
	if err := ValidateManagedRefreshTarget(value); err != nil {
		return nil, "", err
	}
	if s.options.Coordinate == nil {
		return nil, "", errors.New("managed refresh coordinator is not configured")
	}
	return s.options.Coordinate(ctx, value, s.options.CacheKey(value), s.refresh)
}
func (s *ManagedRefreshService) refresh(ctx context.Context, value *Record) (*Record, string, error) {
	if err := ValidateManagedRefreshTarget(value); err != nil {
		return nil, "", err
	}
	expected := FailureVersion(value).CredentialVersion
	observed, err := s.options.Exchange(ctx, value)
	if err != nil {
		if value.IsOpenAI() {
			s.options.Privacy.EnsureOpenAIPrivacy(ctx, value)
		}
		return nil, "", err
	}
	credentials := CloneValues(observed.Credentials)
	if value.Platform == PlatformAntigravity {
		if project, _ := credentials["project_id"].(string); project == "" {
			if old := strings.TrimSpace(value.GetCredential("project_id")); old != "" {
				credentials["project_id"] = old
			}
		}
		if observed.ProjectIDMissing {
			updated, err := s.options.Store.UpdateAccount(ctx, value.ID, &UpdateAccountInput{Credentials: credentials, ExpectedCredentials: &expected})
			if errors.Is(err, ErrRefreshAccountStateChanged) {
				return s.current(ctx, value.ID)
			}
			if err != nil {
				return nil, "", fmt.Errorf("failed to update credentials: %w", err)
			}
			s.options.Privacy.EnsureAntigravityPrivacy(ctx, updated)
			return updated, "missing_project_id_temporary", nil
		}
		if value.Status == StatusError && strings.Contains(value.ErrorMessage, "missing_project_id:") {
			recovery, ok := s.options.Store.(ManagedCredentialRecovery)
			if !ok {
				return nil, "", ErrManagedRecoveryUnavailable
			}
			_, applied, err := recovery.ClearManagedRefreshError(ctx, value)
			if err != nil {
				return nil, "", fmt.Errorf("failed to clear account error: %w", err)
			}
			if !applied {
				return s.current(ctx, value.ID)
			}
			expected.Status = StatusActive
		}
	}
	updated, err := s.options.Store.UpdateAccount(ctx, value.ID, &UpdateAccountInput{Credentials: credentials, ExpectedCredentials: &expected})
	if errors.Is(err, ErrRefreshAccountStateChanged) {
		return s.current(ctx, value.ID)
	}
	if err != nil {
		return nil, "", err
	}
	if s.options.Invalidate != nil {
		if err := s.options.Invalidate(ctx, updated); err != nil {
			s.options.Log("[WARN] Failed to invalidate token cache for account %d: %v", updated.ID, err)
		}
	}
	s.options.Privacy.EnsureOpenAIPrivacy(ctx, updated)
	s.options.Privacy.EnsureAntigravityPrivacy(ctx, updated)
	return updated, "", nil
}

// current 在 CAS 冲突后只回读，不二次交换或继续旧成功副作用。
func (s *ManagedRefreshService) current(ctx context.Context, id int64) (*Record, string, error) {
	current, err := s.options.Store.GetAccount(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if current == nil || (!current.IsOAuth() && !current.IsQoderCosy()) || current.IsCredentialShadow() {
		return nil, "", ErrRefreshAccountStateChanged
	}
	return current, "", nil
}

// ClearError 保留原状态清理与尽力 token 失效顺序，供单项及批量管理共用。
func (s *ManagedRefreshService) ClearError(ctx context.Context, id int64) (*Record, error) {
	value, err := s.options.Store.ClearAccountError(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.options.Invalidate != nil {
		if err := s.options.Invalidate(ctx, value); err != nil {
			s.options.Log("[WARN] Failed to invalidate token cache for account %d: %v", id, err)
		}
	}
	return value, nil
}
