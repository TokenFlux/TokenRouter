package settings

// OmittedKeys 记录普通值字段的缺省存在性；nil 表示完整文档更新。
type OmittedKeys map[string]struct{}

// DropFrom 在准备阶段移除省略字段，不改动存储或已发布配置。
func (o OmittedKeys) DropFrom(values map[string]string) {
	for key := range o {
		delete(values, key)
	}
}
