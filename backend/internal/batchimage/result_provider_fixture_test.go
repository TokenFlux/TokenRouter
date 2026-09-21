//go:build unit

package batchimage_test

import (
	"context"
	"io"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

type publicBatchImageProvider struct {
	name           string
	submits        []batchimage.BatchImageInput
	submitErr      error
	cancelCount    int
	cancelErr      error
	result         string
	cleanupTargets []batchimage.CleanupTarget
	cleanupErr     error
}

func (p *publicBatchImageProvider) Name() string { return p.name }

func (p *publicBatchImageProvider) SupportsAccount(*accountcore.Record) bool { return true }

func (p *publicBatchImageProvider) Submit(_ context.Context, _ *batchimage.BatchImageJob, _ *accountcore.Record, input batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	p.submits = append(p.submits, input)
	if p.submitErr != nil {
		return nil, p.submitErr
	}
	return &batchimage.BatchProviderJob{
		ProviderJobName:   "providers/" + p.name + "/job",
		ProviderInputRef:  "files/" + p.name + "/input",
		ProviderOutputRef: "files/" + p.name + "/output",
	}, nil
}

func (p *publicBatchImageProvider) Get(context.Context, *batchimage.BatchImageJob, *accountcore.Record) (*batchimage.BatchProviderStatus, error) {
	return &batchimage.BatchProviderStatus{InternalState: batchimage.BatchProviderStateQueued}, nil
}

func (p *publicBatchImageProvider) Cancel(context.Context, *batchimage.BatchImageJob, *accountcore.Record) error {
	p.cancelCount++
	return p.cancelErr
}

func (p *publicBatchImageProvider) OpenResult(context.Context, *batchimage.BatchImageJob, *accountcore.Record) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(p.result)), "application/jsonl", nil
}

func (p *publicBatchImageProvider) Cleanup(_ context.Context, _ *batchimage.BatchImageJob, _ *accountcore.Record, target batchimage.CleanupTarget) error {
	p.cleanupTargets = append(p.cleanupTargets, target)
	return p.cleanupErr
}
