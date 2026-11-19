package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// dingTalkAttrDefRepoStub stores user attribute definitions in memory and
// optionally injects a creation failure for a configured key.
type dingTalkAttrDefRepoStub struct {
	UserAttributeDefinitionRepository                                    // unused repository methods
	defs                              map[int64]*UserAttributeDefinition // stored definitions keyed by ID
	failCreateKey                     string                             // key that triggers injected create error
	nextID                            int64                              // next assigned definition identifier
}

// Create stores a copy of def and assigns an incrementing ID unless def.Key
// matches failCreateKey.
func (r *dingTalkAttrDefRepoStub) Create(_ context.Context, def *UserAttributeDefinition) error {
	if def.Key == r.failCreateKey {
		return errors.New("create failed")
	}
	r.nextID++
	def.ID = r.nextID
	copyDef := *def
	r.defs[def.ID] = &copyDef
	return nil
}

// GetByID returns the stored definition for id or ErrAttributeDefinitionNotFound.
func (r *dingTalkAttrDefRepoStub) GetByID(_ context.Context, id int64) (*UserAttributeDefinition, error) {
	if def, ok := r.defs[id]; ok {
		copyDef := *def
		return &copyDef, nil
	}
	return nil, ErrAttributeDefinitionNotFound
}

// GetByKey returns the stored definition matching key or ErrAttributeDefinitionNotFound.
func (r *dingTalkAttrDefRepoStub) GetByKey(_ context.Context, key string) (*UserAttributeDefinition, error) {
	for _, def := range r.defs {
		if def.Key == key {
			copyDef := *def
			return &copyDef, nil
		}
	}
	return nil, ErrAttributeDefinitionNotFound
}

// Update replaces the stored definition matching def.ID.
func (r *dingTalkAttrDefRepoStub) Update(_ context.Context, def *UserAttributeDefinition) error {
	copyDef := *def
	r.defs[def.ID] = &copyDef
	return nil
}

// ExistsByKey reports whether key already exists in defs.
func (r *dingTalkAttrDefRepoStub) ExistsByKey(_ context.Context, key string) (bool, error) {
	_, err := r.GetByKey(context.Background(), key)
	return err == nil, nil
}

// TestEnsureDingTalkSyncAttributesReconcilesInternalOnlyDefinitions exercises
// EnsureDingTalkSyncAttributes under unrestricted and internal_only policies,
// including an existing definition rename and an injected creation failure. It
// checks that non-internal_only skips storage, existing names update, and a
// failed creation does not block subsequent enabled attributes.
func TestEnsureDingTalkSyncAttributesReconcilesInternalOnlyDefinitions(t *testing.T) {
	repo := &dingTalkAttrDefRepoStub{
		defs: map[int64]*UserAttributeDefinition{
			1: {ID: 1, Key: "dingtalk_name", Name: "Old Name", Type: AttributeTypeText, Enabled: true},
		},
		failCreateKey: "dingtalk_email",
		nextID:        1,
	}
	attrs := NewUserAttributeService(repo, nil)

	EnsureDingTalkSyncAttributes(context.Background(), attrs, AdminSettings{
		DingTalkConnectCorpRestrictionPolicy: "none",
		DingTalkConnectSyncDept:              true,
		DingTalkConnectSyncDeptAttrKey:       "dingtalk_dept",
		DingTalkConnectSyncDeptAttrName:      "Dept",
	})
	require.Len(t, repo.defs, 1)

	EnsureDingTalkSyncAttributes(context.Background(), attrs, AdminSettings{
		DingTalkConnectCorpRestrictionPolicy:   "internal_only",
		DingTalkConnectSyncDisplayName:         true,
		DingTalkConnectSyncDisplayNameAttrKey:  "dingtalk_name",
		DingTalkConnectSyncDisplayNameAttrName: "New Name",
		DingTalkConnectSyncCorpEmail:           true,
		DingTalkConnectSyncCorpEmailAttrKey:    "dingtalk_email",
		DingTalkConnectSyncCorpEmailAttrName:   "Corp Email",
		DingTalkConnectSyncDept:                true,
		DingTalkConnectSyncDeptAttrKey:         "dingtalk_dept",
		DingTalkConnectSyncDeptAttrName:        "Department",
	})
	require.Equal(t, "New Name", repo.defs[1].Name)
	dept, err := repo.GetByKey(context.Background(), "dingtalk_dept")
	require.NoError(t, err)
	require.Equal(t, "Department", dept.Name)
	require.Equal(t, AttributeTypeText, dept.Type)
}
