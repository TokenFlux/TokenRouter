package creative

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// targetQueryFixture 记录单型号校验和设置保存读取的规则及存储。
type targetQueryRunRepository struct{ CreativeRunRepository }

type targetQueryFixture struct {
	groupReads, providerReads, inventoryReads int
	checked                                   []string
}

func (f *targetQueryFixture) CanBindGroup(int64, bool) bool                      { return true }
func (f *targetQueryFixture) GetByID(context.Context, int64) (UserAccess, error) { return f, nil }
func (f *targetQueryFixture) GetByIDLite(context.Context, int64) (*GroupView, error) {
	f.groupReads++
	return &GroupView{ID: 1, Active: true, AllowImageGeneration: true, Operations: map[string][]string{PlatformOpenAI: {CreativeOperationGenerate}}}, nil
}

func (f *targetQueryFixture) ListActive(ctx context.Context) ([]GroupView, error) {
	group, err := f.GetByIDLite(ctx, 1)
	return []GroupView{*group}, err
}

func (f *targetQueryFixture) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]CatalogProvider, error) {
	f.providerReads++
	return []CatalogProvider{f}, nil
}
func (f *targetQueryFixture) PlatformID() string { return PlatformOpenAI }
func (f *targetQueryFixture) AllowsProtocol(protocol.ProtocolID, map[protocol.ProtocolID][]protocol.ProtocolID) bool {
	return true
}
func (f *targetQueryFixture) IsSchedulable() bool { return true }
func (f *targetQueryFixture) GetModelMapping() map[string]string {
	f.inventoryReads++
	return map[string]string{"irrelevant": "gpt-image-1"}
}

func (f *targetQueryFixture) GetConfiguredRequestModels() []string {
	f.inventoryReads++
	return []string{"gpt-image-1", "gpt-image-2"}
}
func (f *targetQueryFixture) ResolveMappedModel(model string) (string, bool) { return model, false }
func (f *targetQueryFixture) IsModelSupported(model string) bool {
	f.checked = append(f.checked, model)
	return true
}
func (f *targetQueryFixture) IsCreativeEnabled(context.Context) bool { return true }
func (f *targetQueryFixture) GetCreativeModelSettings(context.Context) []CreativeModelSetting {
	return []CreativeModelSetting{{GroupID: 1, Model: "gpt-image-2", Operations: []string{CreativeOperationGenerate}}}
}

func newTargetQueryPublic(fixture *targetQueryFixture) *Public {
	return &Public{Repo: &targetQueryRunRepository{}, ProviderRepo: fixture, UserRepo: fixture, GroupRepo: fixture, Settings: fixture, Options: PublicOptions{Enabled: true}, ImageUnitPrice: func(context.Context, *GroupView, string, string) (float64, bool) { return 0.1, true }}
}
