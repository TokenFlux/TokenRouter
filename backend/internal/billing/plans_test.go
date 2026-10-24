package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// plans_test.go checks the group selection a plan write declares against the adapter
// that reports stored groups. A deleted group leaves its identifier in plans that already
// record it, so the service confirms the submitted selection before storage receives it.

// planWriteGroupValidator records requested identifiers and returns the configured result,
// letting a scenario inspect which selection a plan write submitted.
type planWriteGroupValidator struct {
	// err is returned from every validation call; nil accepts the selection.
	err error
	// calls holds each validated selection in call order.
	calls [][]int64
}

// planWriteStore records plan writes that reach storage.
type planWriteStore struct {
	// created holds creation requests accepted by the store.
	created []CreatePlanRequest
	// updated holds update requests accepted by the store.
	updated []UpdatePlanRequest
}

// ValidatePlanGroupIDs records the requested identifiers and returns the configured result.
func (v *planWriteGroupValidator) ValidatePlanGroupIDs(_ context.Context, ids []int64) error {
	v.calls = append(v.calls, append([]int64(nil), ids...))
	return v.err
}

// ListPlans returns no stored plans for write scenarios.
func (s *planWriteStore) ListPlans(context.Context) ([]*SubscriptionPlan, error) {
	return nil, nil
}

// ListPlansForSale returns no offered plans for write scenarios.
func (s *planWriteStore) ListPlansForSale(context.Context) ([]*SubscriptionPlan, error) {
	return nil, nil
}

// CreatePlan records the request and returns the plan it describes.
func (s *planWriteStore) CreatePlan(_ context.Context, req CreatePlanRequest) (*SubscriptionPlan, error) {
	s.created = append(s.created, req)
	return &SubscriptionPlan{Name: req.Name, GroupIDs: req.GroupIDs}, nil
}

// UpdatePlan records the request and returns the replaced plan value.
func (s *planWriteStore) UpdatePlan(_ context.Context, id int64, req UpdatePlanRequest) (*SubscriptionPlan, error) {
	s.updated = append(s.updated, req)
	return &SubscriptionPlan{ID: id}, nil
}

// DeletePlan accepts the requested deletion for write scenarios.
func (s *planWriteStore) DeletePlan(context.Context, int64) error {
	return nil
}

// GetPlan reports a stored plan for the requested identity.
func (s *planWriteStore) GetPlan(_ context.Context, id int64) (*SubscriptionPlan, error) {
	return &SubscriptionPlan{ID: id}, nil
}

// newPlanWriteRequest returns creation terms that pass local validation, so a scenario can
// focus on the declared group selection.
func newPlanWriteRequest(groupID int64, groupIDs []int64) CreatePlanRequest {
	return CreatePlanRequest{
		Name:         "Max",
		Price:        20,
		ValidityDays: 30,
		ValidityUnit: "day",
		GroupID:      groupID,
		GroupIDs:     groupIDs,
	}
}

// TestPlansCreatePlanRejectsDeletedGroup supplies a selection whose group no longer exists.
// The write must report the group failure and leave storage untouched.
func TestPlansCreatePlanRejectsDeletedGroup(t *testing.T) {
	store := &planWriteStore{}
	groups := &planWriteGroupValidator{err: apperror.NotFound("GROUP_NOT_FOUND", "group not found")}
	service := NewPlans(store, nil, groups)

	_, err := service.CreatePlan(context.Background(), newPlanWriteRequest(0, []int64{15, 21}))

	require.Equal(t, "GROUP_NOT_FOUND", apperror.Reason(err))
	require.Empty(t, store.created, "a rejected selection must not reach storage")
}

// TestPlansCreatePlanValidatesMergedLegacyGroup supplies a legacy identifier beside a list
// that repeats it. The validator must receive the distinct selection storage will record, and
// an accepted selection must reach the store.
func TestPlansCreatePlanValidatesMergedLegacyGroup(t *testing.T) {
	store := &planWriteStore{}
	groups := &planWriteGroupValidator{}
	service := NewPlans(store, nil, groups)

	_, err := service.CreatePlan(context.Background(), newPlanWriteRequest(7, []int64{9, 7}))

	require.NoError(t, err)
	require.Equal(t, [][]int64{{7, 9}}, groups.calls)
	require.Len(t, store.created, 1)
}

// TestPlansCreatePlanSkipsLookupWithoutGroups supplies no group selection, which leaves the
// plan unrestricted. The validator must not run, and creation must still reach storage.
func TestPlansCreatePlanSkipsLookupWithoutGroups(t *testing.T) {
	store := &planWriteStore{}
	groups := &planWriteGroupValidator{}
	service := NewPlans(store, nil, groups)

	_, err := service.CreatePlan(context.Background(), newPlanWriteRequest(0, nil))

	require.NoError(t, err)
	require.Empty(t, groups.calls)
	require.Len(t, store.created, 1)
}

// TestPlansCreatePlanRejectsMissingValidator supplies a group selection without a configured
// validator. The service cannot confirm the selection, so the write fails as a server-side
// failure before storage.
func TestPlansCreatePlanRejectsMissingValidator(t *testing.T) {
	store := &planWriteStore{}
	service := NewPlans(store, nil, nil)

	_, err := service.CreatePlan(context.Background(), newPlanWriteRequest(0, []int64{21}))

	require.Equal(t, "PLAN_GROUP_VALIDATOR_UNAVAILABLE", apperror.Reason(err))
	require.Empty(t, store.created)
}

// TestPlansUpdatePlanRejectsDeletedGroup supplies a replacement selection that names a
// deleted group. The update must report the group failure and leave storage untouched.
func TestPlansUpdatePlanRejectsDeletedGroup(t *testing.T) {
	store := &planWriteStore{}
	groups := &planWriteGroupValidator{err: apperror.NotFound("GROUP_NOT_FOUND", "group not found")}
	service := NewPlans(store, nil, groups)
	groupIDs := []int64{15, 21}

	_, err := service.UpdatePlan(context.Background(), 7, UpdatePlanRequest{GroupIDs: &groupIDs})

	require.Equal(t, "GROUP_NOT_FOUND", apperror.Reason(err))
	require.Empty(t, store.updated)
}

// TestPlansUpdatePlanValidatesLegacyGroupWithList supplies a legacy identifier beside a
// submitted list. The validator must confirm the merge that storage performs, so the request
// reaching storage keeps the legacy identifier and list that produce the same selection.
func TestPlansUpdatePlanValidatesLegacyGroupWithList(t *testing.T) {
	store := &planWriteStore{}
	groups := &planWriteGroupValidator{}
	service := NewPlans(store, nil, groups)
	groupID := int64(7)
	groupIDs := []int64{21}

	_, err := service.UpdatePlan(context.Background(), 7, UpdatePlanRequest{GroupID: &groupID, GroupIDs: &groupIDs})

	require.NoError(t, err)
	require.Equal(t, [][]int64{{7, 21}}, groups.calls)
	require.Len(t, store.updated, 1)
	// Storage repeats this merge, so the confirmed selection and the stored selection agree
	// only while the submitted parts stay unchanged.
	require.Equal(t, groupID, *store.updated[0].GroupID)
	require.Equal(t, groupIDs, *store.updated[0].GroupIDs)
}

// TestPlansUpdatePlanSkipsLookupWhenGroupsOmitted supplies a price-only change to a plan whose
// recorded selection may name a deleted group. The update carries no group selection, so it
// must reach storage without a lookup that could block an unrelated edit.
func TestPlansUpdatePlanSkipsLookupWhenGroupsOmitted(t *testing.T) {
	store := &planWriteStore{}
	groups := &planWriteGroupValidator{}
	service := NewPlans(store, nil, groups)
	price := 25.0

	_, err := service.UpdatePlan(context.Background(), 7, UpdatePlanRequest{Price: &price})

	require.NoError(t, err)
	require.Empty(t, groups.calls)
	require.Len(t, store.updated, 1)
	require.Nil(t, store.updated[0].GroupIDs, "an omitted selection reaches storage as omitted")
}

// TestPlansUpdatePlanReportsLookupFailure supplies a selection whose validation fails with a
// storage error. The update must propagate that failure so callers can retry instead of
// treating the selection as confirmed.
func TestPlansUpdatePlanReportsLookupFailure(t *testing.T) {
	store := &planWriteStore{}
	lookupErr := errors.New("group lookup unavailable")
	groups := &planWriteGroupValidator{err: lookupErr}
	service := NewPlans(store, nil, groups)
	groupIDs := []int64{21}

	_, err := service.UpdatePlan(context.Background(), 7, UpdatePlanRequest{GroupIDs: &groupIDs})

	require.ErrorIs(t, err, lookupErr)
	require.Empty(t, store.updated)
}
