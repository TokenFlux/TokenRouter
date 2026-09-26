package apikey_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type compositeAPIKeyRepoStub struct {
	apikey.APIKeyRepository
	key           *apikey.APIKey
	updated       *apikey.APIKey
	updatedFields []apikey.APIKeyUpdateFields
}

func (s *compositeAPIKeyRepoStub) GetByID(_ context.Context, _ int64) (*apikey.APIKey, error) {
	copyKey := *s.key
	copyKey.CompositeGroups = apikey.KeyCloneCompositeBindings(s.key.CompositeGroups)
	return &copyKey, nil
}

func (s *compositeAPIKeyRepoStub) Update(_ context.Context, key *apikey.APIKey, fields apikey.APIKeyUpdateFields) error {
	copyKey := *key
	copyKey.CompositeGroups = apikey.KeyCloneCompositeBindings(key.CompositeGroups)
	s.updated = &copyKey
	s.key = &copyKey
	s.updatedFields = append(s.updatedFields, fields)
	return nil
}

type compositeUserRepoStub struct {
	identity.UserRepository

	user *identity.User
}

func (s *compositeUserRepoStub) GetByID(_ context.Context, _ int64) (*identity.User, error) {
	copyUser := *s.user
	return &copyUser, nil
}

type compositeGroupRepoStub struct {
	routing.GroupRepository

	groups map[int64]*routing.Group
}

func (s *compositeGroupRepoStub) GetByID(_ context.Context, id int64) (*routing.Group, error) {
	group, ok := s.groups[id]
	if !ok {
		return nil, routing.ErrGroupNotFound
	}
	copyGroup := *group
	return &copyGroup, nil
}

func TestAPIKeyResolveCompositeModel(t *testing.T) {
	key := &apikey.APIKey{IsComposite: true, CompositeGroups: []apikey.APIKeyCompositeGroup{
		{GroupID: 1, Prefix: "GPT", NormalizedPrefix: "gpt"},
	}}

	binding, model, err := key.ResolveCompositeModel("gPt/vendor/model")
	require.NoError(t, err)
	require.Equal(t, int64(1), binding.GroupID)
	require.Equal(t, "vendor/model", model)

	_, _, err = key.ResolveCompositeModel("vendor/model")
	require.ErrorIs(t, err, apikey.ErrCompositeKeyPrefixNotFound)
	_, _, err = key.ResolveCompositeModel("missing-prefix")
	require.ErrorIs(t, err, apikey.ErrCompositeKeyPrefixRequired)
	_, _, err = key.ResolveCompositeModel("bad prefix/model")
	require.ErrorIs(t, err, apikey.ErrCompositeKeyPrefixInvalid)
}

func TestValidateCompositeGroupInputs(t *testing.T) {
	_, err := apikey.KeyValidateCompositeGroupInputs(nil)
	require.ErrorIs(t, err, apikey.ErrCompositeKeyGroupsRequired)

	tooMany := make([]apikey.APIKeyCompositeGroupInput, apikey.MaxCompositeAPIKeyGroups+1)
	for index := range tooMany {
		tooMany[index] = apikey.APIKeyCompositeGroupInput{GroupID: int64(index + 1), Prefix: "p" + string(rune('a'+index))}
	}
	_, err = apikey.KeyValidateCompositeGroupInputs(tooMany)
	require.ErrorIs(t, err, apikey.ErrCompositeKeyTooManyGroups)

	_, err = apikey.KeyValidateCompositeGroupInputs([]apikey.APIKeyCompositeGroupInput{
		{GroupID: 1, Prefix: "GPT"}, {GroupID: 2, Prefix: "gpt"},
	})
	require.ErrorIs(t, err, apikey.ErrCompositeKeyPrefixDuplicate)

	_, err = apikey.KeyValidateCompositeGroupInputs([]apikey.APIKeyCompositeGroupInput{
		{GroupID: 1, Prefix: "GPT"}, {GroupID: 1, Prefix: "Claude"},
	})
	require.True(t, errors.Is(err, apikey.ErrCompositeKeyGroupDuplicate))
}

func TestCompositeAPIKeyAuthSnapshotRoundTrip(t *testing.T) {
	key := &apikey.APIKey{
		ID: 10, UserID: 20, Key: "sk-composite", Name: "composite", Status: billing.StatusActive,
		IsComposite: true,
		User:        &identity.User{ID: 20, Status: billing.StatusActive, Role: identity.RoleUser, Balance: 100},
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{
				ID: 30, APIKeyID: 10, GroupID: 40, Prefix: "GPT", NormalizedPrefix: "gpt", SortOrder: 1,
				Group: &routing.Group{
					ID: 40, Name: "OpenAI", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, IsExclusive: true,
					RateMultiplier: 1.25, AllowImageGeneration: true, RPMLimit: 80,
					LongContextPricingEnabled: true,
					ModelPricing: []routing.ModelPricingEntry{{
						Models: []string{"gpt-5.4"}, BillingMode: routing.BillingModeToken,
					}},
					AllowedProtocols: []protocol.ProtocolID{
						protocol.ProtocolOpenAIResponses,
						protocol.ProtocolOpenAIChatCompletions,
					},
				},
			},
		},
	}
	service := testkit.NewService(nil, nil, nil, nil, nil, nil, nil)
	service.Start()

	// 复合映射必须连同完整分组鉴权信息一起写入并还原，不能依赖额外数据库查询。
	snapshot := service.KeySnapshotFromAPIKey(context.Background(), key)
	require.NotNil(t, snapshot)
	require.Equal(t, apikey.KeyApiKeyAuthSnapshotVersion, snapshot.Version)
	require.Len(t, snapshot.CompositeGroups, 1)
	require.Equal(t, capability.PlatformOpenAI, snapshot.CompositeGroups[0].Group.Platform)

	restored := service.KeySnapshotToAPIKey(key.Key, snapshot)
	require.True(t, restored.IsComposite)
	require.Nil(t, restored.GroupID)
	require.Len(t, restored.CompositeGroups, 1)
	require.Equal(t, "GPT", restored.CompositeGroups[0].Prefix)
	require.True(t, restored.CompositeGroups[0].Group.Hydrated)
	require.Equal(t, 1.25, restored.CompositeGroups[0].Group.RateMultiplier)
	require.True(t, restored.CompositeGroups[0].Group.AllowImageGeneration)
	require.True(t, restored.CompositeGroups[0].Group.LongContextPricingEnabled)
	require.Equal(t, key.CompositeGroups[0].Group.ModelPricing, restored.CompositeGroups[0].Group.ModelPricing)
	require.Equal(t, []protocol.ProtocolID{
		protocol.ProtocolOpenAIResponses,
		protocol.ProtocolOpenAIChatCompletions,
	}, restored.CompositeGroups[0].Group.AllowedProtocols)

	binding, model, err := restored.ResolveCompositeModel("gpt/gpt-5")
	require.NoError(t, err)
	require.Equal(t, int64(40), binding.GroupID)
	require.Equal(t, "gpt-5", model)
}

func TestAPIKeyUpdateConvertsBetweenOrdinaryAndComposite(t *testing.T) {
	groupOne := &routing.Group{ID: 1, Name: "OpenAI", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, IsExclusive: true}
	groupTwo := &routing.Group{ID: 2, Name: "Claude", Platform: capability.PlatformAnthropic, Status: billing.StatusActive, IsExclusive: true}
	user := &identity.User{ID: 20, Status: billing.StatusActive, AllowedGroups: []int64{1, 2}, GroupRestrictionsLoaded: true}
	groupID := groupOne.ID
	repo := &compositeAPIKeyRepoStub{key: &apikey.APIKey{
		ID: 10, UserID: user.ID, Key: "sk-convert", Name: "convert", Status: billing.StatusActive,
		GroupID: &groupID, Group: groupOne, User: user,
	}}
	service := testkit.NewService(
		repo,
		&compositeUserRepoStub{user: user},
		&compositeGroupRepoStub{groups: map[int64]*routing.Group{1: groupOne, 2: groupTwo}},
		nil, nil, nil, nil,
	)
	service.Start()

	toComposite := true
	inputs := []apikey.APIKeyCompositeGroupInput{{GroupID: 1, Prefix: "GPT"}, {GroupID: 2, Prefix: "Claude"}}
	converted, err := service.Update(context.Background(), 10, user.ID, apikey.UpdateAPIKeyRequest{
		IsComposite: &toComposite, CompositeGroups: &inputs,
	})
	require.NoError(t, err)
	require.True(t, converted.IsComposite)
	require.Nil(t, converted.GroupID)
	require.Len(t, converted.CompositeGroups, 2)
	require.Equal(t, apikey.APIKeyUpdateFields{GroupID: true, CompositeConfiguration: true}, repo.updatedFields[0])

	// 复合转普通必须显式提供目标分组，不能沿用任意一个复合映射。
	toOrdinary := false
	_, err = service.Update(context.Background(), 10, user.ID, apikey.UpdateAPIKeyRequest{IsComposite: &toOrdinary})
	require.ErrorIs(t, err, apikey.ErrCompositeKeyTargetRequired)
	targetGroupID := int64(2)
	converted, err = service.Update(context.Background(), 10, user.ID, apikey.UpdateAPIKeyRequest{
		IsComposite: &toOrdinary, GroupID: &targetGroupID,
	})
	require.NoError(t, err)
	require.False(t, converted.IsComposite)
	require.Equal(t, targetGroupID, *converted.GroupID)
	require.Empty(t, converted.CompositeGroups)
	require.Equal(t, apikey.APIKeyUpdateFields{GroupID: true, CompositeConfiguration: true}, repo.updatedFields[1])
}

func TestCompositeAPIKeyUpdateAddsMappingsWithoutConfirmation(t *testing.T) {
	groupOne := &routing.Group{ID: 1, Name: "One", Status: billing.StatusActive, IsExclusive: true}
	groupTwo := &routing.Group{ID: 2, Name: "Two", Status: billing.StatusActive, IsExclusive: true}
	user := &identity.User{ID: 20, Status: billing.StatusActive, AllowedGroups: []int64{1, 2}, GroupRestrictionsLoaded: true}
	repo := &compositeAPIKeyRepoStub{key: &apikey.APIKey{
		ID: 10, UserID: user.ID, Key: "sk-composite", Name: "composite", Status: billing.StatusActive, User: user,
	}}
	service := testkit.NewService(
		repo,
		&compositeUserRepoStub{user: user},
		&compositeGroupRepoStub{groups: map[int64]*routing.Group{1: groupOne, 2: groupTwo}},
		nil, nil, nil, nil,
	)
	service.Start()
	toComposite := true
	inputs := []apikey.APIKeyCompositeGroupInput{{GroupID: 1, Prefix: "One"}, {GroupID: 2, Prefix: "Two"}}
	updated, err := service.Update(context.Background(), 10, user.ID, apikey.UpdateAPIKeyRequest{
		IsComposite: &toComposite, CompositeGroups: &inputs,
	})
	require.NoError(t, err)
	require.Len(t, updated.CompositeGroups, 2)
	require.Equal(t, apikey.APIKeyUpdateFields{GroupID: true, CompositeConfiguration: true}, repo.updatedFields[0])
}
