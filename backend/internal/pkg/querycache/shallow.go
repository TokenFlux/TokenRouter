package querycache

// ShallowMap 只隔离顶层映射，保留嵌套值身份；nil 输入仍返回已分配的空映射。
func ShallowMap(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
