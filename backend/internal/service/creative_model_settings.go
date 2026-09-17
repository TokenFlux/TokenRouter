package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"
)

type CreativeModelSetting = creative.CreativeModelSetting

type CreativeModelCandidate = creative.CreativeModelCandidate

func (s *CreativePublicService) NormalizeCreativeModelSettingsForSave(ctx context.Context, input []CreativeModelSetting) ([]CreativeModelSetting, error) {
	return s.nativePublic().NormalizeCreativeModelSettingsForSave(ctx, input)
}

func NormalizeCreativeModelSettings(input []CreativeModelSetting) ([]CreativeModelSetting, error) {
	return creative.NormalizeCreativeModelSettings(input)
}
