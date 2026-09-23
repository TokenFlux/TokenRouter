package bedrock

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 默认模型新增时必须显式补充区域规则或未核实状态，不能悄悄退回字符串前缀推断。
func TestBedrockModelRegionRules_DefaultCatalogAndSourceConsistency(t *testing.T) {
	t.Parallel()
	for _, modelID := range DefaultBedrockModelMapping {
		require.Contains(t, BedrockModelRegionRules, BedrockBaseModelID(modelID))
	}
	for baseID, rule := range BedrockModelRegionRules {
		require.NotEmpty(t, rule.SourceURL, baseID)
		for _, source := range rule.InRegionSources {
			require.Contains(t, rule.DocumentedRegions, source)
		}
		seen := map[string]bool{}
		for _, profile := range rule.GeoProfiles {
			require.Equal(t, baseID, BedrockBaseModelID(profile.Id))
			for _, source := range profile.SourceRegions {
				require.False(t, seen[source], "同一型号来源区域不可同时指向两个地域 ID：%s / %s", baseID, source)
				seen[source] = true
				require.Contains(t, rule.DocumentedRegions, source)
				require.NotContains(t, rule.UnverifiedGeoRegions, source)
			}
		}
		if rule.GlobalProfile.Id != "" {
			require.Equal(t, "global."+baseID, rule.GlobalProfile.Id)
			for _, source := range rule.GlobalProfile.SourceRegions {
				require.Contains(t, rule.DocumentedRegions, source)
			}
		}
	}
}
