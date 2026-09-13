// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

func (g *Group) CustomModelsListEnabled() bool { return groupRules(g).CustomModelsListEnabled() }
