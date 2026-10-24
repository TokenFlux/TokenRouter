package billing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// subscription_service_external_test.go checks how plan group labels are resolved for
// subscriber-facing views. A stored plan can name a group that was deleted after the plan was
// saved, and reading a group can also fail temporarily, so the display distinguishes a
// confirmed absence from an undetermined read.

// groupDisplayReader answers group label reads from configured per-identity results so a
// scenario can mix resolved, missing, and failing identities in one enrichment call.
type groupDisplayReader struct {
	// names holds labels for identities that resolve successfully.
	names map[int64]string
	// errs holds a failure for an identity; a missing entry with no name reports absence.
	errs map[int64]error
}

// emptyGroupDisplayReader reports success without a row for every identity.
type emptyGroupDisplayReader struct{}

// GetByIDLite returns the configured label or failure for id, reporting a not-found error
// for an identity that has neither.
func (r groupDisplayReader) GetByIDLite(_ context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if err, ok := r.errs[id]; ok {
		return nil, err
	}
	if name, ok := r.names[id]; ok {
		return &billing.SubscriptionPlanGroup{ID: id, Name: name}, nil
	}
	return nil, apperror.NotFound("GROUP_NOT_FOUND", "group not found")
}

// TestEnrichSubscriptionPlanGroupsDropsDeletedGroup gives a restricted plan that names a
// resolved group and a deleted one. The deleted identity must leave the display list so no
// subscriber sees a bare identifier for a group they cannot reach.
func TestEnrichSubscriptionPlanGroupsDropsDeletedGroup(t *testing.T) {
	reader := groupDisplayReader{names: map[int64]string{21: "Claude(kiro)"}}
	service := billing.NewSubscriptionService(reader, nil, nil)
	items := []billing.UserSubscription{{Plan: &billing.SubscriptionPlan{
		ID:       7,
		GroupIDs: []int64{15, 21},
	}}}

	service.EnrichSubscriptionPlanGroups(context.Background(), items)

	require.Equal(t, []billing.SubscriptionPlanGroup{{ID: 21, Name: "Claude(kiro)"}}, items[0].Plan.ApplicableGroups)
	require.True(t, items[0].Plan.GroupsRestricted, "a nonempty stored selection still marks the plan restricted")
}

// TestEnrichSubscriptionPlanGroupsKeepsUnreadableGroup gives a restricted plan whose group
// read fails with a storage error. The identity must keep its place with an empty label,
// because a failed read leaves the group's existence undetermined.
func TestEnrichSubscriptionPlanGroupsKeepsUnreadableGroup(t *testing.T) {
	reader := groupDisplayReader{errs: map[int64]error{15: errors.New("connection refused")}}
	service := billing.NewSubscriptionService(reader, nil, nil)
	items := []billing.UserSubscription{{Plan: &billing.SubscriptionPlan{
		ID:       7,
		GroupIDs: []int64{15},
	}}}

	service.EnrichSubscriptionPlanGroups(context.Background(), items)

	require.Equal(t, []billing.SubscriptionPlanGroup{{ID: 15}}, items[0].Plan.ApplicableGroups)
}

// TestEnrichSubscriptionPlanGroupsKeepsAbsentRow gives a reader that reports success with no
// row. An empty result does not confirm deletion, so the identity stays in the display with
// an empty label.
func TestEnrichSubscriptionPlanGroupsKeepsAbsentRow(t *testing.T) {
	service := billing.NewSubscriptionService(emptyGroupDisplayReader{}, nil, nil)
	items := []billing.UserSubscription{{Plan: &billing.SubscriptionPlan{
		ID:       7,
		GroupIDs: []int64{15},
	}}}

	service.EnrichSubscriptionPlanGroups(context.Background(), items)

	require.Equal(t, []billing.SubscriptionPlanGroup{{ID: 15}}, items[0].Plan.ApplicableGroups)
}

// GetByIDLite returns no row and no error, matching a reader that cannot supply the group.
func (emptyGroupDisplayReader) GetByIDLite(context.Context, int64) (*billing.SubscriptionPlanGroup, error) {
	return nil, nil
}
