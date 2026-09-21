//go:build unit

package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/stretchr/testify/require"
)

func TestRedeemService_BatchUpdate_PartialFields(t *testing.T) {
	status := billing.StatusDisabled
	notes := "maintenance window"
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	repo := &redeemRepoStub{}
	svc := billing.NewRedeemService(repo, nil, nil, nil, nil, nil, nil, nil, billing.RedeemRuntime{})

	result, err := svc.BatchUpdate(context.Background(), &billing.RedeemCodeBatchUpdateInput{
		IDs: []int64{1, 2, 2},
		Fields: billing.RedeemCodeBatchUpdateFields{
			Status:    &status,
			ExpiresAt: billing.NullableTimeUpdate{Set: true, Value: &expiresAt},
			Notes:     &notes,
		},
	})

	require.NoError(t, err)
	require.Equal(t, int64(2), result.Updated)
	require.True(t, repo.batchUpdateCalled)
	require.Equal(t, []int64{1, 2}, repo.batchUpdateIDs)
	require.Equal(t, &status, repo.batchUpdateFields.Status)
	require.True(t, repo.batchUpdateFields.ExpiresAt.Set)
	require.WithinDuration(t, expiresAt, *repo.batchUpdateFields.ExpiresAt.Value, time.Second)
	require.Equal(t, &notes, repo.batchUpdateFields.Notes)
	require.Nil(t, repo.batchUpdateFields.Type)
	require.Nil(t, repo.batchUpdateFields.Value)
	require.Nil(t, repo.batchUpdateFields.MaxUses)
	require.Nil(t, repo.batchUpdateFields.PlanID)
}

func TestRedeemService_BatchUpdate_RejectsInvalidID(t *testing.T) {
	repo := &redeemRepoStub{}
	svc := billing.NewRedeemService(repo, nil, nil, nil, nil, nil, nil, nil, billing.RedeemRuntime{})
	notes := "bad id"

	result, err := svc.BatchUpdate(context.Background(), &billing.RedeemCodeBatchUpdateInput{
		IDs:    []int64{1, 0},
		Fields: billing.RedeemCodeBatchUpdateFields{Notes: &notes},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, apperror.IsBadRequest(err))
	require.False(t, repo.batchUpdateCalled)
}

func TestRedeemService_BatchUpdate_RejectsCoreFields(t *testing.T) {
	repo := &redeemRepoStub{}
	svc := billing.NewRedeemService(repo, nil, nil, nil, nil, nil, nil, nil, billing.RedeemRuntime{})
	newValue := 100.0

	result, err := svc.BatchUpdate(context.Background(), &billing.RedeemCodeBatchUpdateInput{
		IDs: []int64{42},
		Fields: billing.RedeemCodeBatchUpdateFields{
			Value: &newValue,
		},
	})

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, apperror.IsBadRequest(err))
	require.False(t, repo.batchUpdateCalled)
}
