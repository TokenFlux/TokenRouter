//go:build unit

package creative

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type deliveryContractStore struct {
	CreativeTransientStore
	loadErr  error
	attempts int
	failures int
	recorded *bool
}

func (s *deliveryContractStore) LoadOutput(context.Context, string, int) ([]byte, error) {
	return nil, s.loadErr
}
func (s *deliveryContractStore) SaveOutput(context.Context, string, int, []byte, time.Duration) error {
	if !*s.recorded {
		return errors.New("success fact missing")
	}
	s.attempts++
	if s.attempts <= s.failures {
		return ErrCreativeTransientUnavailable
	}
	return nil
}

type deliveryContractOutcomes struct {
	recorded bool
	cancel   context.CancelFunc
	metadata []CreativeRunOutput
}

func (s *deliveryContractOutcomes) RecordProviderOutcome(_ context.Context, _ string, _ int64, metadata []CreativeRunOutput, _ time.Time) error {
	s.recorded = true
	s.metadata = metadata
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}
func (s *deliveryContractOutcomes) CompleteProviderOutcome(context.Context, string, float64, bool, time.Time) error {
	return nil
}

type deliveryContractRepo struct {
	CreativeRunRepository
	updates int
	code    string
}

func (r *deliveryContractRepo) UpdateCreativeRunOutput(_ context.Context, _ string, _ int, _ string, _ string, _ int64, _ *time.Time, code, _ string) error {
	r.updates++
	r.code = code
	return nil
}

// 读取故障不能认定永久丢失，只有明确不存在才进入丢失分支。
func TestResultDeliveryDistinguishesUnavailableFromMissing(t *testing.T) {
	output := []*CreativeRunOutput{{Status: CreativeRunOutputStatusSucceeded, OutputIndex: 0}}
	for _, test := range []struct {
		name    string
		failure error
		lost    bool
	}{
		{name: "redis_unavailable", failure: ErrCreativeTransientUnavailable},
		{name: "confirmed_missing", failure: ErrCreativeTransientNotFound, lost: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			delivery := ResultDelivery{Store: &deliveryContractStore{loadErr: test.failure}}
			lost, err := delivery.Lost(context.Background(), "run", output)
			require.Equal(t, test.lost, lost)
			if test.lost {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrCreativeTransientUnavailable)
			}
		})
	}
}

// 只重试临时保存，成功事实必须先于首次保存，耗尽后保留丢失标记。
func TestResultDeliveryRetriesOnlySaveAfterDurableFact(t *testing.T) {
	for _, test := range []struct {
		name               string
		failures, attempts int
		code               string
	}{
		{name: "retry_then_saved", failures: 1, attempts: 2},
		{name: "exhausted", failures: 3, attempts: 3, code: OutputDeliveryLost},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome := &deliveryContractOutcomes{}
			store := &deliveryContractStore{failures: test.failures, recorded: &outcome.recorded}
			repo := &deliveryContractRepo{}
			delivery := ResultDelivery{Outcomes: outcome, Store: store, Repo: repo, TTL: time.Minute}
			require.NoError(t, delivery.Record(context.Background(), "run", 1, []ProviderOutput{{Index: 0, Success: true, Bytes: []byte("image"), Mime: "image/png"}}))
			require.True(t, outcome.recorded)
			require.Len(t, outcome.metadata, 1)
			require.Equal(t, test.attempts, store.attempts)
			require.Equal(t, 1, repo.updates)
			require.Equal(t, test.code, repo.code)
		})
	}
}

// 运行或租约取消后，已记录的事实保留，但不能继续保存或发布交付结果。
func TestResultDeliveryCancellationStopsSaving(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcome := &deliveryContractOutcomes{cancel: cancel}
	store := &deliveryContractStore{recorded: &outcome.recorded}
	repo := &deliveryContractRepo{}
	delivery := ResultDelivery{Outcomes: outcome, Store: store, Repo: repo, TTL: time.Minute}
	require.ErrorIs(t, delivery.Record(ctx, "run", 1, []ProviderOutput{{Index: 0, Success: true, Bytes: []byte("image"), Mime: "image/png"}}), context.Canceled)
	require.True(t, outcome.recorded)
	require.Zero(t, store.attempts)
	require.Zero(t, repo.updates)
}
