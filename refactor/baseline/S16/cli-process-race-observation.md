# 经用户确认的测试夹具竞争

## 已有证据

- 命令及完整结果：`usage-store-calendar-integration.json`、`usage-store-calendar-integration.log.gz`。
- 本轮 1,625 条通过、1 项跳过、2 条失败事件；失败为 `TestS02ProcessModes/cli-setup` 及其父测试，不是两个独立问题。不能将整组记为通过。
- race 报告中，`processOutput.text()` 在 `process_integration_test.go:40` 读取 `bytes.Buffer`，`os/exec` 的输出复制同时调用提升的 `bytes.Buffer.ReadFrom()` 写入。
- `cli-process-race-observation.json` 保存当次堆栈片段和源码摘要。该测试文件与实施基线 HEAD 完全相同，本阶段没有修改它。
- 未单独重跑、未扩展复现、未修改此测试或生产实现。按原计划第 1 节，已请求用户确认是否纳入范围。

## 建议的最小范围

仅修正测试进程输出缓冲的封装，使输出复制与文本读取遵守同一互斥；保留原 CLI setup 输入、子进程、退出状态和安装文件断言。不更改服务生命周期、CLI 行为或业务代码。取得用户确认后再实施并补验。

## 授权与实施

- 2026-09-18 用户回复 `ok`，批准上述范围。
- 缓冲与互斥改为私有具名字段，移除可绕过锁的提升方法；Write 与 text 使用同一把锁。原进程行为断言保留，修复后 race 结果待补。

## 修复后结果

`financial-calendar-integration-race.json` 的受影响存储与进程集合为 74 条通过事件，无失败或跳过。完整 `TestS02ProcessModes`（含 cli-setup、standard/simple 与信号关闭）在 race 下通过；保留原交互、退出和文件断言，未新增供应商或生产环境调用。

## 同轮跳过

`TestUsageLogRepoSuite/TestUsageAnalyticsQueriesExecuteOnPostgreSQL` 因 Honolulu 业务日尚无三个完整小时而按原条件跳过。记录当次限制，未删除条件或将跳过计为通过；这项跳过与输出缓冲竞争无关。
