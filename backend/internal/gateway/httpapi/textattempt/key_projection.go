package textattempt

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// cloneAPIKeyWithGroup 只派生本次回退的分组视图，不改写缓存中的 Key。
func cloneAPIKeyWithGroup(apiKey *apikey.APIKey, group *routing.Group) *apikey.APIKey {
	if apiKey == nil || group == nil {
		return apiKey
	}
	cloned := *apiKey
	groupID := group.ID
	cloned.GroupID = &groupID
	cloned.Group = group
	return &cloned
}
