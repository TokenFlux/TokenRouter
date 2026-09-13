// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
)

const upstreamProtocolsKey = acctcore.UpstreamProtocolsKey

func protocolRecord(a *Account) *acctcore.Record {
	if a == nil {
		return nil
	}
	return &acctcore.Record{Platform: a.Platform, Type: a.Type, Credentials: a.Credentials, Extra: a.Extra, ParentAccountID: a.ParentAccountID}
}
func applyProtocolRecord(a *Account, v *acctcore.Record) {
	if a != nil && v != nil {
		a.Credentials = v.Credentials
		a.Extra = v.Extra
	}
}
func (a *Account) NativeProtocolOptions() []domain.ProtocolID {
	return protocolRecord(a).NativeProtocolOptions()
}

func (a *Account) UpstreamProtocols() []domain.ProtocolID {
	return protocolRecord(a).UpstreamProtocolsForLegacy(a.GetAPIProtocol())
}
func NormalizeAccountProtocols(a *Account) error {
	v := protocolRecord(a)
	err := acctcore.NormalizeAccountProtocols(v)
	applyProtocolRecord(a, v)
	return err
}
