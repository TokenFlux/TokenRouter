package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type compositeAPIKeyRepoStub struct {
	APIKeyRepository
	key           *APIKey
	updated       *APIKey
	updatedFields []APIKeyUpdateFields
}

func (s *compositeAPIKeyRepoStub) GetByID(_ context.Context, _ int64) (*APIKey, error) {
	copyKey := *s.key
	copyKey.CompositeGroups = cloneCompositeBindings(s.key.CompositeGroups)
	return &copyKey, nil
}

func (s *compositeAPIKeyRepoStub) Update(_ context.Context, key *APIKey, fields APIKeyUpdateFields) error {
	copyKey := *key
	copyKey.CompositeGroups = cloneCompositeBindings(key.CompositeGroups)
	s.updated = &copyKey
	s.key = &copyKey
	s.updatedFields = append(s.updatedFields, fields)
	return nil
}

type compositeUserRepoStub struct {
	UserRepository
	user *User
}

func (s *compositeUserRepoStub) GetByID(_ context.Context, _ int64) (*User, error) {
	copyUser := *s.user
	return &copyUser, nil
}

type compositeGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
}

func (s *compositeGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group, ok := s.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	copyGroup := *group
	return &copyGroup, nil
}

type compositeNoticeReaderStub struct {
	notice *DataShareNotice
}

func (s *compositeNoticeReaderStub) GetNotice(context.Context) (*DataShareNotice, error) {
	return s.notice, nil
}

func TestAPIKeyResolveCompositeModel(t *testing.T) {
	key := &APIKey{IsComposite: true, CompositeGroups: []APIKeyCompositeGroup{
		{GroupID: 1, Prefix: "GPT", NormalizedPrefix: "gpt"},
	}}

	binding, model, err := key.ResolveCompositeModel("gPt/vendor/model")
	require.NoError(t, err)
	require.Equal(t, int64(1), binding.GroupID)
	require.Equal(t, "vendor/model", model)

	_, _, err = key.ResolveCompositeModel("vendor/model")
	require.ErrorIs(t, err, ErrCompositeKeyPrefixNotFound)
	_, _, err = key.ResolveCompositeModel("missing-prefix")
	require.ErrorIs(t, err, ErrCompositeKeyPrefixRequired)
	_, _, err = key.ResolveCompositeModel("bad prefix/model")
	require.ErrorIs(t, err, ErrCompositeKeyPrefixInvalid)
}

func TestValidateCompositeGroupInputs(t *testing.T) {
	_, err := validateCompositeGroupInputs(nil)
	require.ErrorIs(t, err, ErrCompositeKeyGroupsRequired)

	tooMany := make([]APIKeyCompositeGroupInput, MaxCompositeAPIKeyGroups+1)
	for index := range tooMany {
		tooMany[index] = APIKeyCompositeGroupInput{GroupID: int64(index + 1), Prefix: "p" + string(rune('a'+index))}
	}
	_, err = validateCompositeGroupInputs(tooMany)
	require.ErrorIs(t, err, ErrCompositeKeyTooManyGroups)

	_, err = validateCompositeGroupInputs([]APIKeyCompositeGroupInput{
		{GroupID: 1, Prefix: "GPT"}, {GroupID: 2, Prefix: "gpt"},
	})
	require.ErrorIs(t, err, ErrCompositeKeyPrefixDuplicate)

	_, err = validateCompositeGroupInputs([]APIKeyCompositeGroupInput{
		{GroupID: 1, Prefix: "GPT"}, {GroupID: 1, Prefix: "Claude"},
	})
	require.True(t, errors.Is(err, ErrCompositeKeyGroupDuplicate))
}

func TestCompositeAPIKeyAuthSnapshotRoundTrip(t *testing.T) {
	confirmedAt := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	key := &APIKey{
		ID: 10, UserID: 20, Key: "sk-composite", Name: "composite", Status: StatusActive,
		IsComposite: true,
		User:        &User{ID: 20, Status: StatusActive, Role: RoleUser, Balance: 100},
		CompositeGroups: []APIKeyCompositeGroup{
			{
				ID: 30, APIKeyID: 10, GroupID: 40, Prefix: "GPT", NormalizedPrefix: "gpt", SortOrder: 1,
				DataSharingNoticeVersion: 2, DataSharingConfirmedAt: &confirmedAt,
				Group: &Group{
					ID: 40, Name: "OpenAI", Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true,
					RateMultiplier: 1.25, AllowImageGeneration: true, RPMLimit: 80,
					LongContextPricingEnabled: true,
					ModelPricing: []ChannelModelPricing{{
						Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken,
					}},
					AllowedClientProtocols: []GroupClientProtocol{
						GroupClientProtocolOpenAIResponses,
						GroupClientProtocolOpenAIChatCompletions,
					},
				},
			},
		},
	}
	service := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)

	// 复合映射必须连同完整分组鉴权信息一起写入并还原，不能依赖额外数据库查询。
	snapshot := service.snapshotFromAPIKey(context.Background(), key)
	require.NotNil(t, snapshot)
	require.Equal(t, apiKeyAuthSnapshotVersion, snapshot.Version)
	require.Len(t, snapshot.CompositeGroups, 1)
	require.Equal(t, PlatformOpenAI, snapshot.CompositeGroups[0].Group.Platform)

	restored := service.snapshotToAPIKey(key.Key, snapshot)
	require.True(t, restored.IsComposite)
	require.Nil(t, restored.GroupID)
	require.Len(t, restored.CompositeGroups, 1)
	require.Equal(t, "GPT", restored.CompositeGroups[0].Prefix)
	require.Equal(t, confirmedAt, *restored.CompositeGroups[0].DataSharingConfirmedAt)
	require.True(t, restored.CompositeGroups[0].Group.Hydrated)
	require.Equal(t, 1.25, restored.CompositeGroups[0].Group.RateMultiplier)
	require.True(t, restored.CompositeGroups[0].Group.AllowImageGeneration)
	require.True(t, restored.CompositeGroups[0].Group.LongContextPricingEnabled)
	require.Equal(t, key.CompositeGroups[0].Group.ModelPricing, restored.CompositeGroups[0].Group.ModelPricing)
	require.Equal(t, []GroupClientProtocol{
		GroupClientProtocolOpenAIResponses,
		GroupClientProtocolOpenAIChatCompletions,
	}, restored.CompositeGroups[0].Group.AllowedClientProtocols)

	binding, model, err := restored.ResolveCompositeModel("gpt/gpt-5")
	require.NoError(t, err)
	require.Equal(t, int64(40), binding.GroupID)
	require.Equal(t, "gpt-5", model)
}

func TestAPIKeyUpdateConvertsBetweenOrdinaryAndComposite(t *testing.T) {
	groupOne := &Group{ID: 1, Name: "OpenAI", Platform: PlatformOpenAI, Status: StatusActive, IsExclusive: true}
	groupTwo := &Group{ID: 2, Name: "Claude", Platform: PlatformAnthropic, Status: StatusActive, IsExclusive: true}
	user := &User{ID: 20, Status: StatusActive, AllowedGroups: []int64{1, 2}, GroupRestrictionsLoaded: true}
	groupID := groupOne.ID
	repo := &compositeAPIKeyRepoStub{key: &APIKey{
		ID: 10, UserID: user.ID, Key: "sk-convert", Name: "convert", Status: StatusActive,
		GroupID: &groupID, Group: groupOne, User: user,
	}}
	service := NewAPIKeyService(
		repo,
		&compositeUserRepoStub{user: user},
		&compositeGroupRepoStub{groups: map[int64]*Group{1: groupOne, 2: groupTwo}},
		nil, nil, nil, nil,
	)

	toComposite := true
	inputs := []APIKeyCompositeGroupInput{{GroupID: 1, Prefix: "GPT"}, {GroupID: 2, Prefix: "Claude"}}
	converted, err := service.Update(context.Background(), 10, user.ID, UpdateAPIKeyRequest{
		IsComposite: &toComposite, CompositeGroups: &inputs,
	})
	require.NoError(t, err)
	require.True(t, converted.IsComposite)
	require.Nil(t, converted.GroupID)
	require.Len(t, converted.CompositeGroups, 2)
	require.Equal(t, APIKeyUpdateFields{
		GroupID:                 true,
		CompositeConfiguration:  true,
		DataSharingConfirmation: true,
	}, repo.updatedFields[0])

	// 复合转普通必须显式提供目标分组，不能沿用任意一个复合映射。
	toOrdinary := false
	_, err = service.Update(context.Background(), 10, user.ID, UpdateAPIKeyRequest{IsComposite: &toOrdinary})
	require.ErrorIs(t, err, ErrCompositeKeyTargetRequired)
	targetGroupID := int64(2)
	converted, err = service.Update(context.Background(), 10, user.ID, UpdateAPIKeyRequest{
		IsComposite: &toOrdinary, GroupID: &targetGroupID,
	})
	require.NoError(t, err)
	require.False(t, converted.IsComposite)
	require.Equal(t, targetGroupID, *converted.GroupID)
	require.Empty(t, converted.CompositeGroups)
	require.Equal(t, APIKeyUpdateFields{GroupID: true, CompositeConfiguration: true}, repo.updatedFields[1])
}

func TestCompositeAPIKeyDataSharingConsentCoversNewMappings(t *testing.T) {
	groupOne := &Group{ID: 1, Name: "Share One", Status: StatusActive, IsExclusive: true, DataSharingEnabled: true}
	groupTwo := &Group{ID: 2, Name: "Share Two", Status: StatusActive, IsExclusive: true, DataSharingEnabled: true}
	user := &User{ID: 20, Status: StatusActive, AllowedGroups: []int64{1, 2}, GroupRestrictionsLoaded: true}
	repo := &compositeAPIKeyRepoStub{key: &APIKey{
		ID: 10, UserID: user.ID, Key: "sk-sharing", Name: "sharing", Status: StatusActive, User: user,
	}}
	service := NewAPIKeyService(
		repo,
		&compositeUserRepoStub{user: user},
		&compositeGroupRepoStub{groups: map[int64]*Group{1: groupOne, 2: groupTwo}},
		nil, nil, nil, nil,
	)
	service.SetDataSharingNoticeReader(&compositeNoticeReaderStub{notice: &DataShareNotice{Version: 3}})

	toComposite := true
	inputs := []APIKeyCompositeGroupInput{{GroupID: 1, Prefix: "One"}, {GroupID: 2, Prefix: "Two"}}
	updated, err := service.Update(context.Background(), 10, user.ID, UpdateAPIKeyRequest{
		IsComposite: &toComposite, CompositeGroups: &inputs,
		DataSharingConfirmed: true, DataSharingNoticeVersion: 3,
	})
	require.NoError(t, err)
	require.Len(t, updated.CompositeGroups, 2)
	for _, binding := range updated.CompositeGroups {
		require.Equal(t, 3, binding.DataSharingNoticeVersion)
		require.NotNil(t, binding.DataSharingConfirmedAt)
	}
	require.Equal(t, APIKeyUpdateFields{
		GroupID:                 true,
		CompositeConfiguration:  true,
		DataSharingConfirmation: true,
	}, repo.updatedFields[0])
}
