// 执行目标组合原生账号记录与本次路线；不是持久化、管理 DTO 或调度快照。
package provider

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ExecutionAccount 的值复制保持记录字段独立；map 的复制仍只发生在原边界。
// Route 不进入账号表、认证或调度缓存，账号规则只由 account.Record 实现。
type ExecutionAccount struct {
	Record account.Record            `json:"-"`
	Route  requeststate.AttemptRoute `json:"-"`
}

// View 供当前调用读取原生规则，保留旧 nil 接收者的判断及方法绑定语义。
func (value *ExecutionAccount) View() *account.Record {
	if value == nil {
		return nil
	}
	return &value.Record
}

// NewExecutionAccount 只在已存在的存储/缓存边界复制，不新增查询或共享状态。
func NewExecutionAccount(value *account.Record) *ExecutionAccount {
	if value == nil {
		return nil
	}
	out := &ExecutionAccount{}
	account.CopyRecordInto(&out.Record, value)
	out.Record.Now = time.Now
	out.Record.LoadLocation = time.LoadLocation
	return out
}
func ExecutionRecord(value *ExecutionAccount) *account.Record {
	if value == nil {
		return nil
	}
	out := account.CloneRecord(&value.Record)
	out.Now = time.Now
	out.LoadLocation = time.LoadLocation
	return out
}

// ApplyExecutionRecord 更新原持有者的记录，保留同一 Record 地址和本次路线。
func ApplyExecutionRecord(out *ExecutionAccount, value *account.Record) {
	if out == nil || value == nil {
		return
	}
	account.CopyRecordInto(&out.Record, value)
	out.Record.Now = time.Now
	out.Record.LoadLocation = time.LoadLocation
}
func ExecutionAccounts(values []account.Record) []ExecutionAccount {
	if values == nil {
		return nil
	}
	out := make([]ExecutionAccount, len(values))
	for i := range values {
		account.CopyRecordInto(&out[i].Record, &values[i])
		out[i].Record.Now = time.Now
		out[i].Record.LoadLocation = time.LoadLocation
	}
	return out
}
func ExecutionRecords(values []ExecutionAccount) []account.Record {
	if values == nil {
		return nil
	}
	out := make([]account.Record, len(values))
	for i := range values {
		account.CopyRecordInto(&out[i], &values[i].Record)
		out[i].Now = time.Now
		out[i].LoadLocation = time.LoadLocation
	}
	return out
}
func ExecutionRecordPointers(values []*ExecutionAccount) []*account.Record {
	if values == nil {
		return nil
	}
	out := make([]*account.Record, len(values))
	for i, value := range values {
		out[i] = ExecutionRecord(value)
	}
	return out
}
func ExecutionModelPolicy(value *ExecutionAccount) ModelPolicy {
	out := ModelPolicy{Record: ExecutionRecord(value)}
	if value != nil {
		out.Route = value.Route
	}
	return out
}
func ExecutionProtocolRecord(value *ExecutionAccount) *account.Record {
	if value == nil {
		return nil
	}
	v := &value.Record
	return &account.Record{Platform: v.Platform, Type: v.Type, Credentials: v.Credentials, Extra: v.Extra, ParentAccountID: v.ParentAccountID}
}
func ExecutionProtocolTarget(value *ExecutionAccount) account.ProtocolTarget {
	out := account.ProtocolTarget{Record: ExecutionProtocolRecord(value)}
	if value != nil {
		out.Protocol = value.Route.Protocol()
	}
	return out
}
func NormalizeExecutionProtocols(value *ExecutionAccount) error {
	record := ExecutionProtocolRecord(value)
	err := account.NormalizeAccountProtocols(record)
	if value != nil && record != nil {
		value.Record.Credentials = record.Credentials
		value.Record.Extra = record.Extra
	}
	return err
}
func ExecutionSnapshot(value *ExecutionAccount) account.AccountSnapshot {
	if value == nil {
		return account.AccountSnapshot{}
	}
	v := ExecutionProtocolRecord(value)
	v.ID = value.Record.ID
	v.Status = value.Record.Status
	v.Schedulable = value.Record.Schedulable
	v.Concurrency = value.Record.Concurrency
	v.Priority = value.Record.Priority
	v.ExpiresAt = value.Record.ExpiresAt
	return (ModelPolicy{Record: v, Route: value.Route}).CandidateSnapshot()
}
func ExecutionCandidatePlan(value *ExecutionAccount) (routing.CandidatePlan, bool) {
	if value == nil {
		return routing.CandidatePlan{}, false
	}
	return value.Route.Candidate()
}
func ExecutionRuntimeConfig(value *ExecutionAccount) *account.RuntimeConfig {
	v := &value.Record
	return &account.RuntimeConfig{Extra: v.Extra, Concurrency: v.Concurrency, SessionWindowStart: v.SessionWindowStart, SessionWindowEnd: v.SessionWindowEnd}
}

// ExecutionCompletionRecord 仅提供同步完成捕获需要的字段，不携带请求路线。
func ExecutionCompletionRecord(value *ExecutionAccount) *account.Record {
	if value == nil {
		return nil
	}
	v := &value.Record
	return &account.Record{ID: v.ID, Name: v.Name, Platform: v.Platform, Type: v.Type, Credentials: v.Credentials, Extra: v.Extra, RateMultiplier: v.RateMultiplier, ParentAccountID: v.ParentAccountID}
}
