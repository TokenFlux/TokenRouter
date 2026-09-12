// 本文件维护 billing 的所属能力；兼容入口复用唯一实现。
package billing

// InitialUserFunds 表达身份用例确定的初始赠送，不累计为充值。
type InitialUserFunds struct{ Balance float64 }
