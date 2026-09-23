package routing

// GroupAllowsResponsesImages 保留四态配置及旧内存分组的历史许可语义。
func GroupAllowsResponsesImages(group *Group) bool {
	return group == nil || group.ResponsesImagePolicy != "" || group.AllowImageGeneration
}
