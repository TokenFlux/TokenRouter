# S14 维护写权限与生命周期

## 十二组资金账本中的初始化/恢复

- 首次管理员：identity/postgres 的 CreateInitialAdmin；只创建余额为零的身份，不调用注册、赠送、充值、返利或通知。setup → app/bootstrap → identity/postgres。保留两次计数、原 SQL 字段和五秒预算。
- simple 初始化：routing/postgres 写默认分组及限定的 Grok 自动创建标记；identity/postgres 写管理员并发与原升级标记。不写余额或消费累计。
- 安全密钥：app/bootstrap → security_secrets，原唯一约束与生成/回读机制未变；不经过普通用户设置接口。
- 备份恢复：backup/provider 的 psql 单事务执行选定归档，是受控数据库恢复权限，不伪装成 billing 充值。维护锁名称 maintenance:database-heavy 不变；不暂停所有业务写入。
- 清理维护命令：只执行 ops 的既定历史入口拒绝分类及删除，不改变资金事实。

## 独立保证与范围

备份 completed 只说明已写出所选内容；默认排除 usage_records 时不含完整历史结算与去重证据。恢复 PostgreSQL 不恢复 Redis、对象或任务临时输入。恢复使用旧格式，异步运行记录必须先持久化；最终 SQL 提交与最终运行记录保存不是同一个事务。

系统操作锁沿用 idempotency_records 的原 scope/key。专用接口使用 processing.response_body 保存所有者代次，普通幂等 API 不变。不同代次即使业务 operation ID 相同也不能相互续租或释放；不是新增跨实例支持承诺。

## 资源拥有与关闭

- backup 构造不启动；唯一 cron 由 StartContext 创建。启动回源、手动/定时任务经同一登记屏障。
- BackupAdmission / SystemMaintenanceAdmission：StopOrder 14，HTTP 请求等待前先拒绝新操作并取消运行工作。
- HTTPRequests：原 StopOrder 15，等待 Handler 和下载响应结束。
- SystemMaintenanceOperations：StopOrder 17，等待维护操作和锁释放。
- BackupService：StopOrder 20，等待归档、恢复、cron 以及清理。
- SQL/Redis/日志仍按原最终依赖阶段释放；任何阶段超时不报告已 drain，也不继续提前关闭共享依赖。

HTTP 五秒与后台三十秒总预算不变。浏览器断开不取消已接受的后台备份/恢复或十五分钟系统更新；应用关闭会取消准备工作。二进制两次 rename 属于必须完成或恢复的小临界区。
