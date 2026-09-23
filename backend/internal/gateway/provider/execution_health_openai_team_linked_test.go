//go:build unit

package provider_test

import (
	"context"
	"net/http"
	"testing"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

const teamLinkedDeactivatedBody = `{"detail":{"code":"deactivated_workspace","message":"This workspace has been deactivated."}}`

type teamLinkedAccountRepoStub struct {
	gatewaytestkit.HealthStoreBase
	teamAccounts []gatewayprovider.ExecutionAccount
	listErr      error
	listCalls    int
	setErrorIDs  []int64
	setErrorMsgs map[int64]string
	failSetError map[int64]error
}

// ListByPlatform 镜像真实仓库语义：仅返回该平台的 active 账户。
func (r *teamLinkedAccountRepoStub) ListByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	out := make([]gatewayprovider.ExecutionAccount, 0, len(r.teamAccounts))
	for _, acc := range r.teamAccounts {
		if acc.Record.Platform == platform && acc.Record.Status == billing.StatusActive {
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

func newTeamLinkedAccount(id int64, teamID string) gatewayprovider.ExecutionAccount {
	return gatewayprovider.ExecutionAccount{Record: account.Record{LoadLocation: time.LoadLocation, ID: id,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": teamID}},
	}
}

// newTeamLinkedFixture: #1 触发者(team-A) #2 同队 #3 异队 #4 apikey #5 影子 #6 同队 #7 同队但已 error
func newTeamLinkedFixture() []gatewayprovider.ExecutionAccount {
	parentID := int64(1)
	shadow := gatewayprovider.ExecutionAccount{Record: account.Record{LoadLocation: time.LoadLocation, ID: 5,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          billing.StatusActive,
		ParentAccountID: &parentID},
	}
	apikey := newTeamLinkedAccount(4, "team-A")
	apikey.Record.Type = capability.AccountTypeAPIKey
	erroredSibling := newTeamLinkedAccount(7, "team-A")
	erroredSibling.Record.Status = account.StatusError
	return []gatewayprovider.ExecutionAccount{
		newTeamLinkedAccount(1, "team-A"),
		newTeamLinkedAccount(2, "team-A"),
		newTeamLinkedAccount(3, "team-B"),
		apikey,
		shadow,
		newTeamLinkedAccount(6, "team-A"),
		erroredSibling,
	}
}

func newTeamLinkedTestService(repo *teamLinkedAccountRepoStub) (*accountprovider.UpstreamHealth,
	*gatewaytestkit.RuntimeBlockRecorder) {

	blocker := &gatewaytestkit.RuntimeBlockRecorder{}
	rl := newUpstreamHealthForTest(repo, &config.Config{}, nil, account.HealthOptions{Block: func(v *account.Record, until time.Time, reason string) {
		blocker.BlockAccountScheduling(gatewayprovider.NewExecutionAccount(v), until, reason)
	}}, nil)

	return rl, blocker
}

func TestTeamLinkedError_FanoutMarksSameTeamAccounts(t *testing.T) {
	repo := &teamLinkedAccountRepoStub{teamAccounts: newTeamLinkedFixture()}
	rl, blocker := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedAccount(1, "team-A")

	shouldDisable := gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &trigger, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil)).StopScheduling

	require.True(t, shouldDisable)
	// fan-out 先标记同队兄弟（#2、#6），触发账户 #1 随后由常规 case 402 标记
	require.Equal(t, []int64{2, 6, 1}, repo.setErrorIDs)
	require.Contains(t, repo.setErrorMsgs[2], "team-linked error triggered by account #1")
	require.Contains(t, repo.setErrorMsgs[6], "team-linked error triggered by account #1")
	require.Contains(t, repo.setErrorMsgs[1], "Workspace deactivated (402)")
	require.NotContains(t, repo.setErrorMsgs[1], "team-linked")
	// 熔断顺序：兄弟账户先于落库全部进程内熔断，触发账户走 auth_error
	require.Equal(t, []string{account.OpenAITeamLinkedErrorBlockReason, account.OpenAITeamLinkedErrorBlockReason, "auth_error"}, blocker.Reasons)
	require.Equal(t, int64(2), blocker.Accounts[0].Record.ID)
	require.Equal(t, int64(6), blocker.Accounts[1].Record.ID)
	require.Equal(t, int64(1), blocker.Accounts[2].Record.ID)
}

func TestTeamLinkedError_GenericPaymentErrorDoesNotFanout(t *testing.T) {
	repo := &teamLinkedAccountRepoStub{teamAccounts: newTeamLinkedFixture()}
	rl, _ := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedAccount(1, "team-A")

	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &trigger, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(`{"error":{"message":"insufficient balance"}}`), nil))

	require.Equal(t, []int64{1}, repo.setErrorIDs)
	require.Contains(t, repo.setErrorMsgs[1], "Payment required (402)")
	require.Zero(t, repo.listCalls)
}

func TestTeamLinkedError_DedupWithinTTL(t *testing.T) {
	repo := &teamLinkedAccountRepoStub{teamAccounts: newTeamLinkedFixture()}
	rl, _ := newTeamLinkedTestService(repo)
	first := newTeamLinkedAccount(1, "team-A")
	second := newTeamLinkedAccount(2, "team-A")

	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &first, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil))
	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &second, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil))

	// 第二次触发被去重：只有 #2 自身经 case 402 标记，未再次 fan-out
	require.Equal(t, []int64{2, 6, 1, 2}, repo.setErrorIDs)
	require.Equal(t, 1, repo.listCalls)
}

func TestTeamLinkedError_APIKeyTriggerDoesNotFanout(t *testing.T) {
	repo := &teamLinkedAccountRepoStub{teamAccounts: newTeamLinkedFixture()}
	rl, _ := newTeamLinkedTestService(repo)
	trigger := newTeamLinkedAccount(4, "team-A")
	trigger.Record.Type = capability.AccountTypeAPIKey

	gatewayprovider.ApplyExecutionHealth(context.Background(), rl, &trigger, gatewayprovider.HealthObservationFromContext(context.Background(), http.StatusPaymentRequired, http.Header{}, []byte(teamLinkedDeactivatedBody), nil))

	require.Equal(t, []int64{4}, repo.setErrorIDs)
	require.Zero(t, repo.listCalls)
}
