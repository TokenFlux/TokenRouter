// Vertex 迁移保留原协议及取消边界，旧入口仅投影。
package service

import (
	accountmodule "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

func (a *Account) IsVertexServiceAccount() bool {
	return a != nil && a.Type == capability.AccountTypeServiceAccount
}
func (a *Account) VertexProjectID() string {
	if a == nil {
		return ""
	}
	return (&accountmodule.Record{Credentials: a.Credentials}).VertexProjectID(func(raw []byte) (string, error) {
		key, err := vertex.ParseVertexServiceAccountJSON(raw)
		if err != nil {
			return "", err
		}
		return key.ProjectID, nil
	})
}
func (a *Account) VertexLocation(model string) string {
	if a == nil {
		return (*accountmodule.Record)(nil).VertexLocation(model)
	}
	return (&accountmodule.Record{Credentials: a.Credentials}).VertexLocation(model)
}
