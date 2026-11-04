# datamanagementd 停用说明

TokenRouter 已停用 data management 守护进程接口，备份和恢复使用内置的 backup 模块。当前主服务不探测或连接守护进程的 Unix Socket；安装旧进程不会恢复数据管理接口。

当前仓库不再提供这个守护进程的源码、安装脚本、systemd 模板或构建产物。维护仍依赖它的历史版本时，应从对应的历史标签获取成套源码和安装资料。

## 核对遗留部署

- 旧服务可能名为 `tokenrouter-datamanagementd`，可通过 `systemctl status` 和 `journalctl -u` 查看运行状态和日志。
- 历史 Socket 路径包括 `/tmp/tokenrouter-datamanagement.sock` 和 `/tmp/sub2api-datamanagement.sock`；当前主服务都不使用。
- 守护进程用 SQLite 保存自身元数据，与 TokenRouter 的 PostgreSQL 主库分开。清理遗留进程前，应确认是否还需保留这些元数据和旧备份文件。
- 历史容器编排可能挂载上述 Socket。确认所有使用者都已升级后，可移除对应挂载并停用旧服务。

当前部署和备份约束见[部署指南](index.md)与[部署与数据库迁移](../../operations/deployment_and_migrations.md)。
