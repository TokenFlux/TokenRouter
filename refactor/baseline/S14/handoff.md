# S14 兼容与后续事项

## S15：HTTP 与设置聚合

- server/routes 仍汇总管理员路由；Backup、DataManagement、System 字段已指向新 HTTP 类型，旧 handler 构造仅用于现存兼容消费者/测试。
- 备份设置通过唯一 settings.Store 读取和写入，旧 SettingService 的其它领域聚合不在本阶段搬迁。
- 旧 data management 仅保留下线 DTO、路径与原因，不恢复已移除的守护进程。

## S16：兼容清理

- service 的 BackupService 包装、NewBackupService/ProvideBackupService、SetMaintenanceDB，以及 repository 的 PgDumper/S3 构造仅投影并委托。生产 Wire 已直接构造新模块，没有第二个 cron、缓存或锁。
- service 的 UpdateService、SystemOperationLock、DataManagement 类型与构造保留必要旧签名；生产维护编排使用 ops/maintenance。
- repository/migrations_compat.go 只为遗留测试提供 ApplyMigrations 委托，迁移执行始终由 infra/postgres 拥有。
- setup/bootstrap 的旧私有标记只在现存集成夹具中保留字面预期；生产初始化不导入旧 service/domain。原 sysutil 重启实现已不存在，不新增第二条退出路径。
- 逐符号消费者见三套 references 数据；反射/字符串脚本引用不属于 Go 类型引用结果，Wire 与文档路径另行核对。

## 长期受控写权限

首次管理员的零余额创建、simple 默认配置和管理员并发补齐、安全密钥引导、数据库恢复、已发布 SQL 数据修正继续按明确入口保留。恢复不是业务充值，不抹除 S04 的单服务进程额度协调边界或 S07 的周期重建恢复限制。

## 回退风险

- B01/B02：撤销后恢复重复 cron、停止后重开、回源/清理超出应用预算的风险。
- B03：撤销后恢复本地备份符号链接越界读写/删除风险。
- B04：撤销后可能把 SQL 失败报告为恢复成功；新实现的输入失败取消也不再存在。
- B05：撤销后恢复接管者不能续租、旧持有者覆盖新锁的风险。回退前先停止维护并等待新认领结束，不能让旧代码处理活跃的新所有者代次。
- B06：撤销后恢复未持久登记便启动数据库恢复的风险。

不删除备份文件或历史恢复记录；不在恢复或二进制替换临界区切换实现。无数据库 schema 或缓存格式降级步骤。
