//go:build unit

package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type teamLinkedAccountRepoStub struct {
	teamAccounts []account.Record
	listErr      error
	listCalls    int
	setErrorIDs  []int64
	setErrorMsgs map[int64]string
	failSetError map[int64]error
}

// ListByPlatform 镜像真实仓库语义：仅返回该平台的 active 账户。
func (r *teamLinkedAccountRepoStub) ListByPlatform(ctx context.Context, platform string) ([]account.Record, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]account.Record, 0, len(r.teamAccounts))
	for _, acc := range r.teamAccounts {
		if acc.Platform == platform && acc.Status == account.StatusActive {
			out = append(out, acc)
		}
	}
	return out, nil
}

func (r *teamLinkedAccountRepoStub) SetError(ctx context.Context, id int64, errorMsg string) error {
	if err, ok := r.failSetError[id]; ok {
		return err
	}
	r.setErrorIDs = append(r.setErrorIDs, id)
	if r.setErrorMsgs == nil {
		r.setErrorMsgs = make(map[int64]string)
	}
	r.setErrorMsgs[id] = errorMsg
	return nil
}

func newTeamLinkedAccount(id int64, teamID string) account.Record {
	return account.Record{
		ID:          id,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      account.StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": teamID},
	}
}

// newTeamLinkedFixture: #1 触发者(team-A) #2 同队 #3 异队 #4 apikey #5 影子 #6 同队 #7 同队但已 error
func newTeamLinkedFixture() []account.Record {
	parentID := int64(1)
	shadow := account.Record{
		ID:              5,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          account.StatusActive,
		ParentAccountID: &parentID,
	}
	apikey := newTeamLinkedAccount(4, "team-A")
	apikey.Type = capability.AccountTypeAPIKey
	erroredSibling := newTeamLinkedAccount(7, "team-A")
	erroredSibling.Status = account.StatusError
	return []account.Record{
		newTeamLinkedAccount(1, "team-A"),
		newTeamLinkedAccount(2, "team-A"),
		newTeamLinkedAccount(3, "team-B"),
		apikey,
		shadow,
		newTeamLinkedAccount(6, "team-A"),
		erroredSibling,
	}
}

func TestTeamLinkedError_DirectCallSkipsTriggerAccount(t *testing.T) {
	// 直调对应 fastpath 调用点：账户级临时不可调度规则短路时联动仍然生效
	repo := &teamLinkedAccountRepoStub{teamAccounts: newTeamLinkedFixture()}
	rl, blocker := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedAccount(1, "team-A")

	rl.HandleWorkspaceDeactivated(context.Background(), &trigger, true)

	require.Equal(t, []int64{2, 6}, repo.setErrorIDs)
	require.Equal(t, []string{account.OpenAITeamLinkedErrorBlockReason, account.OpenAITeamLinkedErrorBlockReason}, blocker.reasons)
}

func TestTeamLinkedError_MissingTeamIDDoesNothing(t *testing.T) {
	repo := &teamLinkedAccountRepoStub{teamAccounts: newTeamLinkedFixture()}
	rl, blocker := newTeamLinkedTestService(repo)
	trigger := account.Record{ID: 9, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: account.StatusActive}

	rl.HandleWorkspaceDeactivated(context.Background(), &trigger, true)

	require.Empty(t, repo.setErrorIDs)
	require.Empty(t, blocker.reasons)
	require.Zero(t, repo.listCalls)
}

func TestTeamLinkedError_SetErrorFailureDoesNotAbortRemaining(t *testing.T) {
	repo := &teamLinkedAccountRepoStub{
		teamAccounts: newTeamLinkedFixture(),
		failSetError: map[int64]error{2: errors.New("db down")},
	}
	rl, blocker := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedAccount(1, "team-A")

	rl.HandleWorkspaceDeactivated(context.Background(), &trigger, true)

	require.Equal(t, []int64{6}, repo.setErrorIDs)
	// 进程内熔断先于落库执行，两个账户都已被熔断
	require.Len(t, blocker.reasons, 2)
}

// 直测原生联动拥有者；列表与写入替身不创建旧服务或第二份缓存。
func newTeamLinkedTestService(repo *teamLinkedAccountRepoStub) (*account.TeamLinkedHealth, *teamBlockRecorder) {
	blocker := &teamBlockRecorder{}
	return account.NewTeamLinkedHealth(repo, account.TeamLinkedOptions{Block: blocker.Block}), blocker
}

type teamBlockRecorder struct {
	accounts []*account.Record
	reasons  []string
}

func (r *teamBlockRecorder) Block(value *account.Record, _ time.Time, reason string) {
	r.accounts = append(r.accounts, value)
	r.reasons = append(r.reasons, reason)
}
