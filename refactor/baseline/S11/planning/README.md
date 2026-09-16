# S11 规划证据

规划基线 beb09fb536d9e3c220dac04e8ffb2d4ffeed628b。未改仓库生产代码或测试。

- initial-source-hashes.json / integrity.json：11771 个已跟踪文件摘要不变。
- normal：go test -count=1 -json ./internal/gateway/... ./internal/service ./internal/handler。
- unit-race：unit 标签、网关/会话/完成处理/错误规则定向正则；命令见当前任务记录。
- integration-race：真实 PostgreSQL/Redis，app Qoder 完整链与 repository GatewayCache，包并发 4。
- original_repro_test.go / overlay.json：仓库外追加测试的 Go overlay，原实现不替换。
- original-repro、live-repro、fixed-repro-race：预期失败复现，不能作为通过。最后一组 race count=3。
- test-summary.json：事件数量含父子测试，各集合重叠，不相加。
- fixed-issues.json：用户批准的 B01—B06；固定清单之外不再排查。

S11 实施开始时归档到阶段证据目录；正式计划保存到 refactor/S11-gateway-orchestration.md，规划时未写入仓库。
