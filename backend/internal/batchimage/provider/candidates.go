package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// CandidateProviders 保留批量任务的三种提供商查询，不提前扩大查询范围。
type CandidateProviders interface {
	ResultProviders
	ListSchedulableByPlatform(context.Context, string) ([]provider.Record, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]provider.Record, error)
}

// Candidates 将查询到的提供商转换为批量任务候选，使用注入的注册表和模型观察函数。
type Candidates struct {
	Source       CandidateProviders
	Registry     *batchimage.Registry[BatchImageProvider]
	ObserveModel func(context.Context, string)
}

type candidateRules struct {
	*provider.Record
	models *provider.ModelRulesSnapshot
}

func (r *Candidates) GetByID(ctx context.Context, id int64) (*batchimage.Candidate, error) {
	value, err := r.Source.GetByID(ctx, id)
	return r.Project(value), err
}

func (r *Candidates) ListSchedulableByPlatform(ctx context.Context, platform string) ([]batchimage.Candidate, error) {
	values, err := r.Source.ListSchedulableByPlatform(ctx, platform)
	return r.project(values), err
}

func (r *Candidates) ListSchedulableByGroupIDAndPlatform(ctx context.Context, id int64, platform string) ([]batchimage.Candidate, error) {
	values, err := r.Source.ListSchedulableByGroupIDAndPlatform(ctx, id, platform)
	return r.project(values), err
}

func (r *Candidates) project(values []provider.Record) []batchimage.Candidate {
	out := make([]batchimage.Candidate, len(values))
	for i := range values {
		out[i] = *r.Project(&values[i])
	}
	return out
}

// Project 为批量图片的本次提供商读取准备模型和协议规则。
func (r *Candidates) Project(value *provider.Record) *batchimage.Candidate {
	if value == nil {
		return nil
	}
	models := provider.PrepareModelRules(value, provideradapter.ModelDefaults(), provideradapter.ModelRules(value))
	snapshot := value.RoutingSnapshot()
	result := &batchimage.Candidate{ID: value.ID, Priority: value.Priority, CandidateRules: candidateRules{Record: value, models: models}}
	result.ProtocolEnabled = func() bool {
		_, ok := routing.Plan(routing.PlanInput{ClientProtocol: protocol.ProtocolImageBatches}).ResolveCandidate(snapshot)
		return ok
	}
	result.SupportsProvider = func(name string) bool {
		selected, ok := r.Registry.Get(name)
		return ok && selected != nil && selected.SupportsProvider(provider.CloneRecord(value))
	}
	result.Bind = func(name string) batchimage.ExecutionProvider {
		selected, _ := r.Registry.Get(name)
		return BindProvider(selected, value)
	}
	// 批量提供商按 Gemini/Vertex 规则解析模型。
	result.ResolveUpstream = func(ctx context.Context, model string) string {
		resolved := strings.TrimSpace(models.ForwardMappedModel(model))
		if r.ObserveModel != nil {
			r.ObserveModel(ctx, resolved)
		}
		return resolved
	}
	return result
}

func (r candidateRules) GetModelMapping() map[string]string {
	return r.models.Mapping()
}

func (r candidateRules) IsModelSupported(model string) bool {
	return r.models.Supports(model)
}

func (r candidateRules) ResolveMappedModel(model string) (string, bool) {
	return r.models.ResolveMappedModel(model)
}

// GetConfiguredRequestModels 提供白名单和映射中的具体型号。
func (r candidateRules) GetConfiguredRequestModels() []string {
	return r.models.ConfiguredModels()
}
