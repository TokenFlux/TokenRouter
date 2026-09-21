package provider

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// CandidateAccounts 保留批量任务的三种账号查询，不提前扩大查询范围。
type CandidateAccounts interface {
	ResultAccounts
	ListSchedulableByPlatform(context.Context, string) ([]account.Record, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]account.Record, error)
}

// Candidates 只把已查询的账号投影为任务候选，注册表与模型观测由装配注入。
type Candidates struct {
	Source       CandidateAccounts
	Registry     *batchimage.Registry[BatchImageProvider]
	ObserveModel func(context.Context, string)
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
func (r *Candidates) project(values []account.Record) []batchimage.Candidate {
	out := make([]batchimage.Candidate, len(values))
	for i := range values {
		out[i] = *r.Project(&values[i])
	}
	return out
}

// Project 按原时机执行资格及一跳模型映射，不把候选写回共享账号数据。
func (r *Candidates) Project(value *account.Record) *batchimage.Candidate {
	if value == nil {
		return nil
	}
	result := &batchimage.Candidate{ID: value.ID, Priority: value.Priority, CandidateRules: candidateRules{value}}
	result.ProtocolEnabled = func() bool {
		_, ok := routing.Plan(routing.PlanInput{ClientProtocol: protocol.ProtocolImageBatches}).ResolveCandidate(value.RoutingSnapshot())
		return ok
	}
	result.SupportsProvider = func(name string) bool {
		selected, ok := r.Registry.Get(name)
		return ok && selected != nil && selected.SupportsAccount(account.CloneRecord(value))
	}
	result.Bind = func(name string) batchimage.ExecutionProvider {
		selected, _ := r.Registry.Get(name)
		return BindAccount(selected, value)
	}
	// 已有批量提供商只执行 Gemini/Vertex，不执行其它平台的模型规范化。
	result.ResolveUpstream = func(ctx context.Context, model string) string {
		resolved := strings.TrimSpace(account.ResolveForwardMappedModel(value, model, accountprovider.ModelDefaults()))
		if r.ObserveModel != nil {
			r.ObserveModel(ctx, resolved)
		}
		return resolved
	}
	return result
}

type candidateRules struct{ *account.Record }

func (r candidateRules) GetModelMapping() map[string]string {
	return account.ResolveModelMapping(r.Record, accountprovider.ModelDefaults())
}
func (r candidateRules) IsModelSupported(model string) bool {
	return r.Record.IsModelSupported(model, accountprovider.ModelDefaults(), accountprovider.ModelRules(r.Record))
}
func (r candidateRules) ResolveMappedModel(model string) (string, bool) {
	return account.ResolveMappedModel(r.Platform, r.GetModelMapping(), model)
}
