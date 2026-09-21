//go:build unit

package identity_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/stretchr/testify/require"
)

// invitationRaceUserRepo 只实现邀请码注册路径所需的用户仓储方法，其余方法复用嵌入接口。
type invitationRaceUserRepo struct {
	identity.UserRepository

	mu      sync.Mutex
	nextID  int64
	byEmail map[string]*identity.User
}

func newInvitationRaceUserRepo() *invitationRaceUserRepo {
	return &invitationRaceUserRepo{
		nextID:  1,
		byEmail: make(map[string]*identity.User),
	}
}

func (r *invitationRaceUserRepo) ExistsByEmail(_ context.Context, email string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.byEmail[email]
	return exists, nil
}

func (r *invitationRaceUserRepo) ExistsByNormalizedEmail(context.Context, string) (bool, error) {
	return false, nil
}

func (r *invitationRaceUserRepo) Create(_ context.Context, user *identity.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byEmail[user.Email]; exists {
		return identity.ErrEmailExists
	}
	user.ID = r.nextID
	r.nextID++
	clone := *user
	r.byEmail[user.Email] = &clone
	return nil
}

// invitationRaceRedeemRepo 使用条件更新语义模拟数据库中的一次性邀请码占用。
type invitationRaceRedeemRepo struct {
	billing.RedeemCodeRepository

	mu   sync.Mutex
	code billing.RedeemCode
}

func (r *invitationRaceRedeemRepo) GetByCode(_ context.Context, code string) (*billing.RedeemCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code.Code != code {
		return nil, billing.ErrRedeemCodeNotFound
	}
	clone := r.code
	return &clone, nil
}

func (r *invitationRaceRedeemRepo) Use(_ context.Context, id, userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code.ID != id {
		return billing.ErrRedeemCodeNotFound
	}
	if r.code.Status != billing.StatusUnused {
		return billing.ErrRedeemCodeUsed
	}
	now := time.Now().UTC()
	r.code.Status = billing.StatusUsed
	r.code.UsedBy = &userID
	r.code.UsedAt = &now
	r.code.UsedCount = 1
	return nil
}

// TestAuthService_Register_InvitationCodeSingleUseUnderConcurrency 确认同一邀请码并发注册只成功一次。
func TestAuthService_Register_InvitationCodeSingleUseUnderConcurrency(t *testing.T) {
	const (
		code   = "INV-RACE-001"
		called = 8
	)
	userRepo := newInvitationRaceUserRepo()
	redeemRepo := &invitationRaceRedeemRepo{code: billing.RedeemCode{
		ID:     1,
		Code:   code,
		Type:   billing.RedeemTypeInvitation,
		Status: billing.StatusUnused,
	}}
	svc := newOAuthEmailFlowAuthService(
		userRepo,
		redeemRepo,
		&refreshTokenCacheStub{},
		map[string]string{
			identity.SettingKeyRegistrationEnabled:    "true",
			promotion.SettingKeyInvitationCodeEnabled: "true",
		},
		nil,
		&userPlatformQuotaRepoStub{},
	)

	start := make(chan struct{})
	results := make(chan error, called)
	var group sync.WaitGroup
	for index := 0; index < called; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, _, err := svc.RegisterWithVerification(
				context.Background(),
				fmt.Sprintf("race-%d@example.com", index),
				"Password123!",
				"",
				"",
				code,
				"",
			)
			results <- err
		}(index)
	}
	close(start)
	group.Wait()
	close(results)

	successes := 0
	rejected := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identity.ErrInvitationCodeInvalid):
			rejected++
		default:
			t.Fatalf("unexpected registration error: %v", err)
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, called-1, rejected)
	redeemRepo.mu.Lock()
	require.Equal(t, billing.StatusUsed, redeemRepo.code.Status)
	redeemRepo.mu.Unlock()
}
