# S05 事务、字段与过渡调用账本

逐文件、声明、测试标签、包级直接消费者和 Wire 引用见 [文件所有权](file-ownership.json.gz)；十二组资金操作继承 S04 底稿并更新 S05 位置，见 [资金账本](funding-writes.json)。这里记录静态 import 不能表达的事务和字段约束。

| 操作 | 编排及规则 | 事务与参与者 | 成功后与失败边界 | 证据 |
| --- | --- | --- | --- | --- |
| 注册与默认赠送 | identity.AuthService；AuthState 读取设置后复查 | identity/postgres.AuthState 拥有原创建事务；billing InitialFunds、RegistrationInvitations、订阅参与相同 Ent context | 保留 savepoint/fail-open、补偿和来源幂等；赠送不作为普通充值累计 | 注册/首次绑定既有 unit；S05 participants integration；最终全量 integration |
| 验证邮箱 OAuth 创建/绑定 | PendingFlow.FinalizeVerifiedAccount | 保留用户创建提交与后续绑定事务；接纳选择在原 Begin 后独立写入 | Begin 失败新增尽力删除补偿；写入/提交失败保留原补偿行为；H06 旧 HEAD 已复现 | original-email-oauth-begin-regression、email-oauth-begin-fixed-integration |
| pending 接纳与消费 | identity.PendingFlow；identity/postgres/pending_binding.go | 复用原 Ent Tx；消费 UPDATE 带 consumed_at IS NULL；竞争失败重读既有 consumed 错误 | OIDC/LinuxDo/DingTalk 使用原组合操作；微信完成注册仍是原独立消费顺序 | H05 原 HEAD 与修复后的真实并发事务；S05 integration race |
| 用户删除及 Key 墓碑 | identity.UserAdmin | AdminMutations.DeleteUserAndKeys → KeyStore.LifecycleInTx；用户关联清理在原事务 | 任意 Key/身份删除失败回滚；数据库触发器自动 outbox，不重复入队 | TestUserDelete、S05 admin participants、原触发器测试 |
| 用户专属分组替换 | identity.UserAdmin.ReplaceUserGroup | 授予新权限 → KeyLifecycleParticipant → 移除旧权限；同 Ent Tx | 任何一步失败回滚；提交后原失效顺序 | s05_admin_participants_integration_test.go |
| 管理 Key 分组+消费重置 | apikey.Admin.UpdateManagedFields | AdminGroupMutations 与 identity.GroupAccessInTx；billing KeyUsageReset 参与同一 UPDATE | 先验证、再写；失败不得留下已重置计数；H03 修复保留单独内部重置兼容 | s05_admin_reset_regression_integration_test.go |
| 团队邀请/成员移除/归属转移 | team.TeamService 与 team/postgres | 原隔离级别与锁顺序；Key 状态经 apikey.TeamKeys.InTx；成员重置经 billing.MemberUsageStore.InTx | 重新加入不恢复旧 Key；owner 禁用标记独立；邮件失败不回滚已持久邀请 | Team integration 套件及 S05 participants |
| 兑换并发数 | billing.RedeemService | identity.ConcurrencyInTx 同连接参与，不新开事务 | 原负数边界和 usage/次数失败整体回滚；提交后才运行原副作用 | s05_participants_integration_test.go；全量兑换 integration |
| 刷新凭据轮换 | identity.SessionService | 原 Redis key 原子 DEL 成功才取得消费权，未新增格式或索引协议 | 用户/version/binding 验证先执行；消费失败不签发；后继签发失败保留旧 token 已失效 | H04 原 HEAD、真实 Redis 竞争和 integration race |
| Passkey | identity.PasskeyService → provider.WebAuthnVerifier | Redis GETDEL 消费后 SDK 解析/验证；凭据存于 PostgreSQL | 损坏报文也不能重放；Origin/签名失败不更新凭据 | TestS05PasskeySDKCeremony：真实 SDK、软件 ES256、Redis/PG |

## 写权限

| 字段族 | 配置/意图 | 实际写入 | 禁止混入的快照 |
| --- | --- | --- | --- |
| 用户 email/password/status/username/avatar/身份/属性/并发配置 | identity | identity/postgres 的显式字段更新 | balance、frozen_balance、total_recharged 及并发消费累计 |
| 注册余额/首次绑定余额、邀请码和订阅权益 | identity 决定授予边界 | billing/postgres 命名参与操作 | 不改变普通充值和累计充值的差别 |
| Key 分组/复合映射/IP/到期/额度配置 | apikey | apikey/postgres.KeyStore.Update 的字段掩码 | 普通更新不覆盖 quota_used、usage_5h/1d/7d 和窗口快照 |
| Key 消费累计和窗口重置 | apikey 决定权限/重置意图，billing 拥有资金规则 | billing/postgres.KeyUsageStore、KeyUsageReset | 配置+重置同请求保持原子性 |
| 成员角色/状态/限額配置 | team | team/postgres；Key 生命周期参与 apikey | 普通限额修改不回写消费字段 |
| 成员消费与窗口 | billing；team 决定管理意图 | billing/postgres.MemberUsageStore 与原结算事务 | 原日/周/月日期边界，不改成滚动窗口 |
| 账号配额配置/累计维护 | 原 account/service/repository | 本阶段保留，S06/S07 继续 | 不宣称全项目资金写入已收敛 |
| Promo/返利转余额/退款/初始化恢复 | 原闭合用例 | 原完整事务留 S12/S14 | 不抽走单条资金语句另行提交 |

所有事务参与者只接受 app/存储 Adapter 提供的原 Ent/SQL 事务；不新增 context key，不自行 Commit/Rollback，不在提交前广播成功失效。金额精度及 S04 平台额度单进程协调边界保持。
