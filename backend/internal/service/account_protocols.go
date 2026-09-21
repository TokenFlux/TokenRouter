// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// accountProtocolTarget 只在旧执行入口尚未清零期间投影本次协议，不保存第二份规则。
func accountProtocolTarget(a *Account) acctcore.ProtocolTarget {
	target := acctcore.ProtocolTarget{Record: protocolRecord(a)}
	if a != nil {
		target.Protocol = a.attemptRoute.Protocol()
	}
	return target
}

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
func (a *Account) NativeProtocolOptions() []protocol.ProtocolID {
	return protocolRecord(a).NativeProtocolOptions()
}

func (a *Account) UpstreamProtocols() []protocol.ProtocolID {
	return protocolRecord(a).UpstreamProtocolsForLegacy(a.GetAPIProtocol())
}
func NormalizeAccountProtocols(a *Account) error {
	v := protocolRecord(a)
	err := acctcore.NormalizeAccountProtocols(v)
	applyProtocolRecord(a, v)
	return err
}
