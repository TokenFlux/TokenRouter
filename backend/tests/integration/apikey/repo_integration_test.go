//go:build integration

package apikey_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// API Key reads combine credential rows with owner and routing relationships,
// while masked edits select which persisted fields can change. Suite cases use
// rollback transactions to inspect creation, group replacement, soft deletion,
// search, usage state, and credential tombstones. Constraint failures run at
// the end of the cases that deliberately abort their PostgreSQL transaction.
//
// The standalone usage-increment case instead obtains a dedicated migrated
// database with committed rows. Ten workers each add one increment through the
// shared repository, store errors in separate indexed slots, and join before
// the final read. This distinguishes its concurrent accumulation evidence from
// the suite cases that reproduce stale snapshots with sequential calls.

// TestAPIKeyRepoSuite runs the PostgreSQL key repository suite with per-case
// rollback fixtures. Its cases inspect stored key values, loaded relationships,
// filtering, and selected update behavior.
func TestAPIKeyRepoSuite(t *testing.T) {
	suite.Run(t, new(APIKeyRepoSuite))
}

// TestCreate inserts an active key through the repository in the suite
// transaction. Creation fills its ID, and a subsequent ID lookup returns the
// supplied credential.
func (s *APIKeyRepoSuite) TestCreate() {
	user := s.mustCreateUser("create@test.com")

	key := &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-create-test",
		Name:   "Test Key",
		Status: billing.StatusActive,
	}

	err := s.repo.Create(s.ctx, key)
	s.Require().NoError(err, "Create")
	s.Require().NotZero(key.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("sk-create-test", got.Key)
}

// TestUpdateForkSpecificFields creates a key and selects Fast mode and
// unavailable-group fallback in its update mask. Reloading returns force_off
// and a disabled fallback flag.
func (s *APIKeyRepoSuite) TestUpdateForkSpecificFields() {
	user := s.mustCreateUser("fork-update-fields@test.com")
	key := &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-fork-update-fields",
		Name:   "Fork Fields",
		Status: billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	loaded, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	loaded.FastModePolicy = apikey.APIKeyFastModePolicyForceOff
	loaded.FallbackWhenGroupUnavailable = false

	// Select both policy fields so their supplied values are persisted together;
	// omitted fields on the loaded object remain outside this configuration edit.
	s.Require().NoError(s.repo.Update(s.ctx, loaded, apikey.APIKeyUpdateFields{
		FastModePolicy:               true,
		FallbackWhenGroupUnavailable: true,
	}))

	updated, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().Equal(apikey.APIKeyFastModePolicyForceOff, updated.FastModePolicy)
	s.Require().False(updated.FallbackWhenGroupUnavailable)
}

// TestCompositeKeyCRUDAndGroupQueries creates, replaces, queries, and clears
// composite mappings within one key. Loaded mapping order, group counts,
// removed entries, and duplicate-group rejection follow the stored
// configuration.
func (s *APIKeyRepoSuite) TestCompositeKeyCRUDAndGroupQueries() {
	user := s.mustCreateUser("composite-key@test.com")
	openAIGroup := s.mustCreateGroup("g-composite-openai")
	claudeGroup := s.mustCreateGroup("g-composite-claude")
	key := &apikey.APIKey{
		UserID: user.ID, Key: "sk-composite-integration", Name: "Composite Key",
		Status: billing.StatusActive, IsComposite: true,
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: openAIGroup.ID, Prefix: "GPT", NormalizedPrefix: "gpt", SortOrder: 0},
			{GroupID: claudeGroup.ID, Prefix: "Claude", NormalizedPrefix: "claude", SortOrder: 1},
		},
	}

	// Store the composite configuration before checking its loaded order and group
	// queries, since those reads depend on persisted mapping rows tied to this
	// key.
	s.Require().NoError(s.repo.Create(s.ctx, key))
	got, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().True(got.IsComposite)
	s.Require().Nil(got.GroupID)
	s.Require().Len(got.CompositeGroups, 2)
	s.Require().Equal("GPT", got.CompositeGroups[0].Prefix)
	s.Require().Equal("Claude", got.CompositeGroups[1].Prefix)
	s.Require().NotNil(got.CompositeGroups[0].Group)

	count, err := s.repo.CountByGroupID(s.ctx, claudeGroup.ID)
	s.Require().NoError(err)
	s.Require().Equal(int64(1), count)
	listed, page, err := s.repo.ListByGroupID(s.ctx, claudeGroup.ID, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err)
	s.Require().Equal(int64(1), page.Total)
	s.Require().Len(listed, 1)
	s.Require().Equal(key.ID, listed[0].ID)

	// Replace the complete mapping list with one entry so the later read can
	// detect a stale relationship left behind by partial binding replacement.
	got.CompositeGroups = []apikey.APIKeyCompositeGroup{
		{GroupID: claudeGroup.ID, Prefix: "Anthropic", NormalizedPrefix: "anthropic", SortOrder: 0},
	}
	s.Require().NoError(s.repo.Update(s.ctx, got, apikey.APIKeyUpdateFields{CompositeConfiguration: true}))
	updated, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().Len(updated.CompositeGroups, 1)
	s.Require().Equal("Anthropic", updated.CompositeGroups[0].Prefix)

	affected, err := s.repo.ClearGroupIDByGroupID(s.ctx, claudeGroup.ID)
	s.Require().NoError(err)
	s.Require().Equal(int64(1), affected)
	cleared, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().Empty(cleared.CompositeGroups)

	// Perform the deliberate duplicate insert after the successful mapping
	// assertions. PostgreSQL aborts this case transaction on a constraint
	// violation until rollback.
	_, err = s.client.APIKeyCompositeGroup.Create().
		SetAPIKeyID(key.ID).
		SetGroupID(openAIGroup.ID).
		SetPrefix("GPT").
		SetNormalizedPrefix("gpt").
		Save(s.ctx)
	s.Require().NoError(err)
	_, err = s.client.APIKeyCompositeGroup.Create().
		SetAPIKeyID(key.ID).
		SetGroupID(openAIGroup.ID).
		SetPrefix("Other").
		SetNormalizedPrefix("other").
		Save(s.ctx)
	s.Require().Error(err)
}

// TestCompositeKeyNormalizedPrefixUniqueConstraint persists one composite
// prefix then inserts that normalized prefix for another group. PostgreSQL
// rejects the duplicate within the same key configuration.
func (s *APIKeyRepoSuite) TestCompositeKeyNormalizedPrefixUniqueConstraint() {
	user := s.mustCreateUser("composite-prefix-unique@test.com")
	firstGroup := s.mustCreateGroup("g-composite-prefix-first")
	secondGroup := s.mustCreateGroup("g-composite-prefix-second")
	key := &apikey.APIKey{
		UserID: user.ID, Key: "sk-composite-prefix-unique", Name: "Composite Prefix Unique",
		Status: billing.StatusActive, IsComposite: true,
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: firstGroup.ID, Prefix: "GPT", NormalizedPrefix: "gpt", SortOrder: 0},
		},
	}

	// Create the first mapping before the deliberate duplicate insertion.
	// PostgreSQL aborts this transaction when the key-level normalized-prefix
	// constraint rejects it.
	s.Require().NoError(s.repo.Create(s.ctx, key))
	_, err := s.client.APIKeyCompositeGroup.Create().
		SetAPIKeyID(key.ID).
		SetGroupID(secondGroup.ID).
		SetPrefix("gpt").
		SetNormalizedPrefix("gpt").
		Save(s.ctx)
	s.Require().Error(err)
}

// TestGetByID_NotFound queries a numeric key ID that the suite fixture has not
// created. The repository returns an error for the missing row.
func (s *APIKeyRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
}

// TestGetByKey stores a key linked to one user and routing group. Credential
// lookup returns its ID with the matching user and group relationships loaded.
func (s *APIKeyRepoSuite) TestGetByKey() {
	user := s.mustCreateUser("getbykey@test.com")
	group := s.mustCreateGroup("g-key")

	key := &apikey.APIKey{
		UserID:  user.ID,
		Key:     "sk-getbykey",
		Name:    "My Key",
		GroupID: &group.ID,
		Status:  billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	got, err := s.repo.GetByKey(s.ctx, key.Key)
	s.Require().NoError(err, "GetByKey")
	s.Require().Equal(key.ID, got.ID)
	s.Require().NotNil(got.User, "expected User preload")
	s.Require().Equal(user.ID, got.User.ID)
	s.Require().NotNil(got.Group, "expected Group preload")
	s.Require().Equal(group.ID, got.Group.ID)
}

// TestGetByKey_NotFound requests a credential absent from the current
// transaction fixture. The repository returns an error when that credential has
// no matching stored row.
func (s *APIKeyRepoSuite) TestGetByKey_NotFound() {
	_, err := s.repo.GetByKey(s.ctx, "non-existent-key")
	s.Require().Error(err, "expected error for non-existent key")
}

// TestGetByKeyForAuth_PreservesSelectedGroupFields sets protocol, mapped-model,
// advanced-scheduler, and Fast controls on a group. Authentication lookup
// returns those asserted fields on the key group view.
func (s *APIKeyRepoSuite) TestGetByKeyForAuth_PreservesSelectedGroupFields() {
	user := s.mustCreateUser("getbykey-auth-dispatch@test.com")
	// Authentication loads the selected group's routing policies.
	lbTopK := 4
	group, err := s.client.Group.Create().
		SetName("g-auth-dispatch").
		SetStatus(billing.StatusActive).
		SetRateMultiplier(1).
		SetSchedulerType(string(routing.GroupSchedulerTypeAdvanced)).
		SetAdvancedSchedulerOverrides(routing.GroupAdvancedSchedulerOverrides{LBTopK: &lbTopK}).
		SetForceOpenaiFast(true).
		SetAllowedProtocols([]protocol.ProtocolID{
			protocol.ProtocolAnthropicMessages,
			protocol.ProtocolOpenAIResponses,
			protocol.ProtocolOpenAIChatCompletions,
		}).
		SetAllowMessagesDispatch(true).
		SetDefaultMappedModel("gpt-5.4").
		Save(s.ctx)
	s.Require().NoError(err)

	key := &apikey.APIKey{
		UserID:  user.ID,
		Key:     "sk-getbykey-auth-dispatch",
		Name:    "Dispatch Key",
		GroupID: &group.ID,
		Status:  billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	got, err := s.repo.GetByKeyForAuth(s.ctx, key.Key)
	s.Require().NoError(err)
	s.Require().NotNil(got.Group)
	s.Require().Equal([]protocol.ProtocolID{
		protocol.ProtocolAnthropicMessages,
		protocol.ProtocolOpenAIResponses,
		protocol.ProtocolOpenAIChatCompletions,
	}, got.Group.AllowedProtocols)
	s.Require().Equal("gpt-5.4", got.Group.DefaultMappedModel)
	s.Require().Equal(routing.GroupSchedulerTypeAdvanced, got.Group.SchedulerType)
	s.Require().NotNil(got.Group.AdvancedSchedulerOverrides.LBTopK)
	s.Require().Equal(4, *got.Group.AdvancedSchedulerOverrides.LBTopK)
	s.Require().True(got.Group.ForceOpenAIFast)
}

// TestUpdate selects name and status on an existing key edit. Reloading
// reflects those changes and retains the previously stored credential and owner
// ID.
func (s *APIKeyRepoSuite) TestUpdate() {
	user := s.mustCreateUser("update@test.com")
	key := &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-update",
		Name:   "Original",
		Status: billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	key.Name = "Renamed"
	key.Status = billing.StatusDisabled
	err := s.repo.Update(s.ctx, key, apikey.APIKeyUpdateFields{Name: true, Status: true})
	s.Require().NoError(err, "Update")

	got, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("sk-update", got.Key, "Update should not change key")
	s.Require().Equal(user.ID, got.UserID, "Update should not change user_id")
	s.Require().Equal("Renamed", got.Name)
	s.Require().Equal(billing.StatusDisabled, got.Status)
}

// TestUpdate_ClearGroupID sets a loaded key group to nil and selects GroupID in
// the mask. Reloading confirms that the ordinary group assignment is cleared.
func (s *APIKeyRepoSuite) TestUpdate_ClearGroupID() {
	user := s.mustCreateUser("cleargroup@test.com")
	group := s.mustCreateGroup("g-clear")
	key := &apikey.APIKey{
		UserID:  user.ID,
		Key:     "sk-clear-group",
		Name:    "Group Key",
		GroupID: &group.ID,
		Status:  billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	key.GroupID = nil
	err := s.repo.Update(s.ctx, key, apikey.APIKeyUpdateFields{GroupID: true})
	s.Require().NoError(err, "Update")

	got, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err)
	s.Require().Nil(got.GroupID, "expected GroupID to be cleared")
}

// TestDelete removes an existing key through the repository within the suite
// transaction. A later ID lookup returns an error for the deleted record.
func (s *APIKeyRepoSuite) TestDelete() {
	user := s.mustCreateUser("delete@test.com")
	key := &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-delete",
		Name:   "Delete Me",
		Status: billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	err := s.repo.Delete(s.ctx, key.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, key.ID)
	s.Require().Error(err, "expected error after delete")
}

// TestCreate_AfterSoftDelete_AllowsSameKey deletes a stored credential and
// creates another object with that credential. The replacement succeeds with a
// nonzero ID distinct from the deleted row.
func (s *APIKeyRepoSuite) TestCreate_AfterSoftDelete_AllowsSameKey() {
	user := s.mustCreateUser("recreate-after-soft-delete@test.com")
	const reusedKey = "sk-reuse-after-soft-delete"

	first := &apikey.APIKey{
		UserID: user.ID,
		Key:    reusedKey,
		Name:   "First Key",
		Status: billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, first), "create first key")

	s.Require().NoError(s.repo.Delete(s.ctx, first.ID), "soft delete first key")

	second := &apikey.APIKey{
		UserID: user.ID,
		Key:    reusedKey,
		Name:   "Second Key",
		Status: billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, second), "create second key with same key")
	s.Require().NotZero(second.ID)
	s.Require().NotEqual(first.ID, second.ID, "recreated key should be a new row")
}

// TestListByUserID creates two keys for one owner in the suite transaction. The
// user listing returns both rows and a total count of two.
func (s *APIKeyRepoSuite) TestListByUserID() {
	user := s.mustCreateUser("listbyuser@test.com")
	s.mustCreateApiKey(user.ID, "sk-list-1", "Key 1", nil)
	s.mustCreateApiKey(user.ID, "sk-list-2", "Key 2", nil)

	keys, page, err := s.repo.ListByUserID(s.ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, apikey.APIKeyListFilters{})
	s.Require().NoError(err, "ListByUserID")
	s.Require().Len(keys, 2)
	s.Require().Equal(int64(2), page.Total)
}

// TestListByUserID_Pagination creates five keys and requests a page containing
// two rows. The result has two keys while metadata retains total five and three
// pages.
func (s *APIKeyRepoSuite) TestListByUserID_Pagination() {
	user := s.mustCreateUser("paging@test.com")
	for i := range 5 {
		s.mustCreateApiKey(user.ID, "sk-page-"+string(rune('a'+i)), "Key", nil)
	}

	keys, page, err := s.repo.ListByUserID(s.ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 2}, apikey.APIKeyListFilters{})
	s.Require().NoError(err)
	s.Require().Len(keys, 2)
	s.Require().Equal(int64(5), page.Total)
	s.Require().Equal(3, page.Pages)
}

// TestCountByUserID inserts two keys for the same owner in the suite
// transaction. Counting by that user returns two after both insertions succeed.
func (s *APIKeyRepoSuite) TestCountByUserID() {
	user := s.mustCreateUser("count@test.com")
	s.mustCreateApiKey(user.ID, "sk-count-1", "K1", nil)
	s.mustCreateApiKey(user.ID, "sk-count-2", "K2", nil)

	count, err := s.repo.CountByUserID(s.ctx, user.ID)
	s.Require().NoError(err, "CountByUserID")
	s.Require().Equal(int64(2), count)
}

// TestListByGroupID creates two assigned keys and one unassigned key for the
// same user. The group listing returns two rows, total two, and a loaded user.
func (s *APIKeyRepoSuite) TestListByGroupID() {
	user := s.mustCreateUser("listbygroup@test.com")
	group := s.mustCreateGroup("g-list")

	s.mustCreateApiKey(user.ID, "sk-grp-1", "K1", &group.ID)
	s.mustCreateApiKey(user.ID, "sk-grp-2", "K2", &group.ID)
	// Keep this comparison key unassigned so the group query can exclude it while
	// returning the two credentials whose bindings match the target group.
	s.mustCreateApiKey(user.ID, "sk-grp-3", "K3", nil)

	keys, page, err := s.repo.ListByGroupID(s.ctx, group.ID, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "ListByGroupID")
	s.Require().Len(keys, 2)
	s.Require().Equal(int64(2), page.Total)
	s.Require().NotNil(keys[0].User)
}

// TestCountByGroupID creates one key assigned to a routing group in the suite
// transaction. Counting by that group returns one for the stored assignment.
func (s *APIKeyRepoSuite) TestCountByGroupID() {
	user := s.mustCreateUser("countgroup@test.com")
	group := s.mustCreateGroup("g-count")
	s.mustCreateApiKey(user.ID, "sk-gc-1", "K1", &group.ID)

	count, err := s.repo.CountByGroupID(s.ctx, group.ID)
	s.Require().NoError(err, "CountByGroupID")
	s.Require().Equal(int64(1), count)
}

// TestExistsByKey stores one credential and checks both that value and an
// absent value. Existence returns true for the stored key and false for the
// other.
func (s *APIKeyRepoSuite) TestExistsByKey() {
	user := s.mustCreateUser("exists@test.com")
	s.mustCreateApiKey(user.ID, "sk-exists", "K", nil)

	exists, err := s.repo.ExistsByKey(s.ctx, "sk-exists")
	s.Require().NoError(err, "ExistsByKey")
	s.Require().True(exists)

	notExists, err := s.repo.ExistsByKey(s.ctx, "sk-not-exists")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

// TestSearchAPIKeys stores Production and Development names for one owner.
// Searching the prod fragment returns a single row whose name contains
// Production in the suite fixture.
func (s *APIKeyRepoSuite) TestSearchAPIKeys() {
	user := s.mustCreateUser("search@test.com")
	s.mustCreateApiKey(user.ID, "sk-search-1", "Production Key", nil)
	s.mustCreateApiKey(user.ID, "sk-search-2", "Development Key", nil)

	found, err := s.repo.SearchAPIKeys(s.ctx, user.ID, "prod", 10)
	s.Require().NoError(err, "SearchAPIKeys")
	s.Require().Len(found, 1)
	s.Require().Contains(found[0].Name, "Production")
}

// TestSearchAPIKeys_NoKeyword stores two keys and supplies an empty search
// keyword. The repository returns both keys for the selected owner within the
// result limit.
func (s *APIKeyRepoSuite) TestSearchAPIKeys_NoKeyword() {
	user := s.mustCreateUser("searchnokw@test.com")
	s.mustCreateApiKey(user.ID, "sk-nk-1", "K1", nil)
	s.mustCreateApiKey(user.ID, "sk-nk-2", "K2", nil)

	found, err := s.repo.SearchAPIKeys(s.ctx, user.ID, "", 10)
	s.Require().NoError(err)
	s.Require().Len(found, 2)
}

// TestSearchAPIKeys_NoUserID stores a matching key and passes zero as the owner
// filter. The search still returns the matching row in the current transaction.
func (s *APIKeyRepoSuite) TestSearchAPIKeys_NoUserID() {
	user := s.mustCreateUser("searchnouid@test.com")
	s.mustCreateApiKey(user.ID, "sk-nu-1", "TestKey", nil)

	found, err := s.repo.SearchAPIKeys(s.ctx, 0, "testkey", 10)
	s.Require().NoError(err)
	s.Require().Len(found, 1)
}

// TestClearGroupIDByGroupID creates two assigned keys and one unassigned row.
// Bulk clearing reports two affected rows, clears both assignments, and leaves
// the group count zero.
func (s *APIKeyRepoSuite) TestClearGroupIDByGroupID() {
	user := s.mustCreateUser("cleargrp@test.com")
	group := s.mustCreateGroup("g-clear-bulk")

	k1 := s.mustCreateApiKey(user.ID, "sk-clr-1", "K1", &group.ID)
	k2 := s.mustCreateApiKey(user.ID, "sk-clr-2", "K2", &group.ID)
	// Keep an unassigned comparison key so the affected count measures the two
	// bound credentials whose group assignments the bulk clear actually removes.
	s.mustCreateApiKey(user.ID, "sk-clr-3", "K3", nil)

	affected, err := s.repo.ClearGroupIDByGroupID(s.ctx, group.ID)
	s.Require().NoError(err, "ClearGroupIDByGroupID")
	s.Require().Equal(int64(2), affected)

	got1, _ := s.repo.GetByID(s.ctx, k1.ID)
	got2, _ := s.repo.GetByID(s.ctx, k2.ID)
	s.Require().Nil(got1.GroupID)
	s.Require().Nil(got2.GroupID)

	count, _ := s.repo.CountByGroupID(s.ctx, group.ID)
	s.Require().Zero(count)
}

// TestCRUD_Search_ClearGroupID follows credential lookup, a masked edit, user
// listing, search, and group clearing. Assertions check retained identity,
// edited values, matching counts, and the cleared assignment.
func (s *APIKeyRepoSuite) TestCRUD_Search_ClearGroupID() {
	user := s.mustCreateUser("k@example.com")
	group := s.mustCreateGroup("g-k")
	key := s.mustCreateApiKey(user.ID, "sk-test-1", "My Key", &group.ID)
	key.GroupID = &group.ID

	got, err := s.repo.GetByKey(s.ctx, key.Key)
	s.Require().NoError(err, "GetByKey")
	s.Require().Equal(key.ID, got.ID)
	s.Require().NotNil(got.User)
	s.Require().Equal(user.ID, got.User.ID)
	s.Require().NotNil(got.Group)
	s.Require().Equal(group.ID, got.Group.ID)

	key.Name = "Renamed"
	key.Status = billing.StatusDisabled
	key.GroupID = nil
	s.Require().NoError(s.repo.Update(s.ctx, key, apikey.APIKeyUpdateFields{Name: true, Status: true, GroupID: true}), "Update")

	got2, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("sk-test-1", got2.Key, "Update should not change key")
	s.Require().Equal(user.ID, got2.UserID, "Update should not change user_id")
	s.Require().Equal("Renamed", got2.Name)
	s.Require().Equal(billing.StatusDisabled, got2.Status)
	s.Require().Nil(got2.GroupID)

	keys, page, err := s.repo.ListByUserID(s.ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, apikey.APIKeyListFilters{})
	s.Require().NoError(err, "ListByUserID")
	s.Require().Equal(int64(1), page.Total)
	s.Require().Len(keys, 1)

	exists, err := s.repo.ExistsByKey(s.ctx, "sk-test-1")
	s.Require().NoError(err, "ExistsByKey")
	s.Require().True(exists, "expected key to exist")

	found, err := s.repo.SearchAPIKeys(s.ctx, user.ID, "renam", 10)
	s.Require().NoError(err, "SearchAPIKeys")
	s.Require().Len(found, 1)
	s.Require().Equal(key.ID, found[0].ID)

	// Clearing a group must update both the key and the group count.
	k2 := s.mustCreateApiKey(user.ID, "sk-test-2", "Group Key", &group.ID)
	k2.GroupID = &group.ID

	countBefore, err := s.repo.CountByGroupID(s.ctx, group.ID)
	s.Require().NoError(err, "CountByGroupID")
	s.Require().Equal(int64(1), countBefore, "expected 1 key in group before clear")

	affected, err := s.repo.ClearGroupIDByGroupID(s.ctx, group.ID)
	s.Require().NoError(err, "ClearGroupIDByGroupID")
	s.Require().Equal(int64(1), affected, "expected 1 affected row")

	got3, err := s.repo.GetByID(s.ctx, k2.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Nil(got3.GroupID, "expected GroupID cleared")

	countAfter, err := s.repo.CountByGroupID(s.ctx, group.ID)
	s.Require().NoError(err, "CountByGroupID after clear")
	s.Require().Equal(int64(0), countAfter, "expected 0 keys in group after clear")
}

// TestIncrementQuotaUsed_Basic records successive usage increments on a newly
// created key. Returned cumulative values are 1.5 after the first write and 4
// after the second.
func (s *APIKeyRepoSuite) TestIncrementQuotaUsed_Basic() {
	user := s.mustCreateUser("incr-basic@test.com")
	key := s.mustCreateApiKey(user.ID, "sk-incr-basic", "Incr", nil)

	newQuota, err := s.usage.IncrementQuotaUsed(s.ctx, key.ID, 1.5)
	s.Require().NoError(err, "IncrementQuotaUsed")
	s.Require().Equal(1.5, newQuota, "第一次递增后应为 1.5")

	newQuota, err = s.usage.IncrementQuotaUsed(s.ctx, key.ID, 2.5)
	s.Require().NoError(err, "IncrementQuotaUsed second")
	s.Require().Equal(4.0, newQuota, "第二次递增后应为 4.0")
}

// TestIncrementQuotaUsed_NotFound adds quota usage to an ID absent from the
// suite fixture. The repository returns APIKeyNotFound for that attempted
// increment operation.
func (s *APIKeyRepoSuite) TestIncrementQuotaUsed_NotFound() {
	_, err := s.usage.IncrementQuotaUsed(s.ctx, 999999, 1.0)
	s.Require().ErrorIs(err, apikey.ErrAPIKeyNotFound, "不存在的 key 应返回 ErrAPIKeyNotFound")
}

// TestIncrementQuotaUsed_DeletedKey soft deletes a key before attempting a
// usage increment. The repository returns APIKeyNotFound even though the
// deleted storage row previously existed.
func (s *APIKeyRepoSuite) TestIncrementQuotaUsed_DeletedKey() {
	user := s.mustCreateUser("incr-deleted@test.com")
	key := s.mustCreateApiKey(user.ID, "sk-incr-del", "Deleted", nil)

	s.Require().NoError(s.repo.Delete(s.ctx, key.ID), "Delete")

	_, err := s.usage.IncrementQuotaUsed(s.ctx, key.ID, 1.0)
	s.Require().ErrorIs(err, apikey.ErrAPIKeyNotFound, "已删除的 key 应返回 ErrAPIKeyNotFound")
}

// TestIncrementQuotaUsedAndGetState starts with usage below a quota and adds an
// amount exceeding it. Returned and reloaded states record 3.5 usage with
// quota_exhausted status.
func (s *APIKeyRepoSuite) TestIncrementQuotaUsedAndGetState() {
	user := s.mustCreateUser("quota-state@test.com")
	key := s.mustCreateApiKey(user.ID, "sk-quota-state", "QuotaState", nil)
	key.Quota = 3
	key.QuotaUsed = 1
	s.Require().NoError(s.repo.Update(s.ctx, key, apikey.APIKeyUpdateFields{Quota: true, QuotaUsed: true}), "Update quota")

	state, err := s.usage.IncrementQuotaUsedAndGetState(s.ctx, key.ID, 2.5)
	s.Require().NoError(err, "IncrementQuotaUsedAndGetState")
	s.Require().NotNil(state)
	s.Require().Equal(3.5, state.QuotaUsed)
	s.Require().Equal(3.0, state.Quota)
	s.Require().Equal(apikey.StatusAPIKeyQuotaExhausted, state.Status)
	s.Require().Equal(key.Key, state.Key)

	got, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal(3.5, got.QuotaUsed)
	s.Require().Equal(apikey.StatusAPIKeyQuotaExhausted, got.Status)
}

// TestIncrementQuotaUsed_Concurrent performs ten PostgreSQL usage increments
// through committed rows shared by its workers. Every write succeeds and the
// final usage equals their summed increments.
func TestIncrementQuotaUsed_Concurrent(t *testing.T) {
	integrationDB, client := keyDatabase(t)
	repo := newKeyStoreFixture(client, integrationDB)
	usage := billingpostgres.NewKeyUsageStore(client, integrationDB)
	ctx := context.Background()

	u, err := client.User.Create().
		SetEmail("concurrent-incr-" + time.Now().Format(time.RFC3339Nano) + "@test.com").
		SetPasswordHash("hash").
		SetStatus(billing.StatusActive).
		SetRole(identity.RoleUser).
		Save(ctx)
	require.NoError(t, err, "create user")

	k := &apikey.APIKey{
		UserID: u.ID,
		Key:    "sk-concurrent-" + time.Now().Format(time.RFC3339Nano),
		Name:   "Concurrent",
		Status: billing.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, k), "create api key")
	// Remove the dependent key before its owner once test workers have joined;
	// fixture cleanup discards deletion errors before the database itself is
	// closed.
	t.Cleanup(func() {
		_ = client.APIKey.DeleteOneID(k.ID).Exec(ctx)
		_ = client.User.DeleteOneID(u.ID).Exec(ctx)
	})

	// Ten writers provide a bounded concurrent accumulation case.
	const goroutines = 10
	// Each writer contributes one identical quota usage unit.
	const increment = 1.0
	// Each worker owns one error slot. Join all workers before reading those slots
	// or final usage so neither assertion can observe an unfinished increment.
	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = usage.IncrementQuotaUsed(ctx, k.ID, increment)
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		require.NoError(t, e, "goroutine %d failed", i)
	}

	got, err := repo.GetByID(ctx, k.ID)
	require.NoError(t, err, "GetByID")
	require.Equal(t, float64(goroutines)*increment, got.QuotaUsed,
		"并发递增后总和应为 %v，实际为 %v", float64(goroutines)*increment, got.QuotaUsed)
}

// TestDeleteWithAudit_TombstonesWithoutRetainingCredential deletes an existing
// key, then inspects its raw row and audit count. ID lookup fails, the
// credential is tombstoned, and deleted-key audits remain empty.
func (s *APIKeyRepoSuite) TestDeleteWithAudit_TombstonesWithoutRetainingCredential() {
	user := s.mustCreateUser("delwithaudit@test.com")
	key := &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-del-audit-1",
		Name:   "Audit Me",
		Status: billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	s.Require().NoError(s.repo.DeleteWithAudit(s.ctx, key.ID))

	_, err := s.repo.GetByID(s.ctx, key.ID)
	s.Require().Error(err)

	var tombstone string
	var deletedAt time.Time
	rows, err := s.client.QueryContext(s.ctx, `SELECT key, deleted_at FROM api_keys WHERE id = $1`, key.ID)
	s.Require().NoError(err)
	s.Require().True(rows.Next())
	s.Require().NoError(rows.Scan(&tombstone, &deletedAt))
	// Close the inspected raw-key rows before the next query uses this single
	// transaction connection, avoiding an outstanding result on the shared
	// executor.
	s.Require().NoError(rows.Close())
	s.Require().NotEqual("sk-del-audit-1", tombstone)
	s.Require().Contains(tombstone, "__deleted__")

	var auditCount int
	auditRows, err := s.client.QueryContext(s.ctx,
		`SELECT COUNT(*) FROM deleted_api_key_audits WHERE api_key_id = $1`, key.ID)
	s.Require().NoError(err)
	s.Require().True(auditRows.Next())
	s.Require().NoError(auditRows.Scan(&auditCount))
	// Finish the audit query before case rollback closes the transaction; the
	// explicit close result is asserted so a cleanup failure is visible here.
	s.Require().NoError(auditRows.Close())
	s.Require().Zero(auditCount, "deleted credentials must not be retained")
}

// TestDeleteWithAudit_RepeatIsIdempotent calls the deletion operation twice for
// the same created key. Both calls succeed after the first has already deleted
// the stored row.
func (s *APIKeyRepoSuite) TestDeleteWithAudit_RepeatIsIdempotent() {
	user := s.mustCreateUser("delwithaudit-idem@test.com")
	key := &apikey.APIKey{UserID: user.ID, Key: "sk-del-audit-2", Name: "K", Status: billing.StatusActive}
	s.Require().NoError(s.repo.Create(s.ctx, key))

	s.Require().NoError(s.repo.DeleteWithAudit(s.ctx, key.ID))
	s.Require().NoError(s.repo.DeleteWithAudit(s.ctx, key.ID))
}

// TestDeleteWithAudit_NotFound calls the deletion operation for an ID absent
// from the suite fixture. It returns APIKeyNotFound for the requested missing
// storage record.
func (s *APIKeyRepoSuite) TestDeleteWithAudit_NotFound() {
	err := s.repo.DeleteWithAudit(s.ctx, 999999)
	s.Require().ErrorIs(err, apikey.ErrAPIKeyNotFound)
}
