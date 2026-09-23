# S16 验证资料

本目录保存 S16 的规划基线、迁移记录、测试结果和阶段性提交检查。S16 仍在实施，完整阶段验收尚未完成；计划与执行记录见 [S16-final-cleanup.md](../../S16-final-cleanup.md)。

## 归档与索引

- `s16-evidence.tar.gz`：本次提交前的 2,030 份完整验证文件，包含规划材料、通过、失败、跳过及后续复核记录。原始文件共 925,574,964 字节，压缩归档为 58,858,023 字节。
- `archive-manifest.json`：归档与每个原始文件的路径、字节数和 SHA-256；已逐文件读取归档内容并核对全部 2,030 个摘要。
- `interim-search-catalogue-check.json`：最近一轮合并检查的命令、日志位置和退出码；完整输出收录在归档中。
- `commit-verification.json` 与 `commit-*.log.gz`：本次分批提交重新运行的编译检查、目录与路由 race、三套 lint，以及 E2E 编译和 Makefile 入口检查。

原始验证文件仍保留在本地原路径，未删除或覆盖。版本库保存压缩归档，目录中的 Markdown 记录保留为可直接阅读的文件。新增验证记录需在后续提交时更新归档与校验清单。

## 读取完整证据

从新的检出目录读取原始日志时，可先解压到独立临时目录：

```bash
evidence_dir="$(mktemp -d)"
tar -xzf refactor/baseline/S16/s16-evidence.tar.gz -C "$evidence_dir"
```

归档内路径从 `refactor/baseline/S16/` 开始，可据此定位阶段记录引用的原始文件。单份提交检查日志可用 `gzip -cd refactor/baseline/S16/commit-catalogue-race.log.gz` 读取。

## 验证范围

本次使用 Go 1.27.0。普通、unit、integration 的 `go test -run=^$ -p=4` 用于编译检查，不计为业务测试通过；目录与路由的 unit race 取得 399 条通过事件，无失败或跳过，三套 lint 均退出零。

既有合并检查日志分别记录普通、unit、integration 的测试事件：通过 11,877 / 19,912 / 12,874，跳过 4 / 8 / 5，无失败事件或失败包。集合存在重叠，不求和；这些阶段性记录不替代 S16 最终验收。E2E 本次仅验证编译与命令入口，未连接真实供应商执行。

## 2026-09-23 网关清理检查点

`progress-20260923-manifest.json` 记录相对原归档新增或变化的完整证据及分卷 SHA-256；每个分卷都已重新读取并逐文件验哈希。先解压原 `s16-evidence.tar.gz`，再按各检查点 manifest 的 `parent_manifest` 顺序应用其 `archives` 列表；同一检查点内按分卷编号排序，即可还原当前证据；原始文件仍保留在本地。

最新稳定合并检查为普通 11,986、unit 20,020、integration 12,988 条通过，既有跳过 4/8/4，三套 lint 为零。此后文本选项与输出的定向 race 分别为 559/741 条通过，当前构建、2,110 条原生包普通补验及 unit lint 通过。各集合有重叠，不相加；S16 和最终验收仍未完成。完整命令与结果见归档内 `interim-native-health-text-checks.json` 和 `progress-20260923-verification.json`。

## 2026-09-23 通用文本运行时检查点

`progress-20260923-textattempt-manifest.json` 是上一检查点的增量索引，按原归档、`progress-20260923-01` 至 `07`、`progress-20260923-textattempt-*` 的顺序解压。所有新增分卷均逐成员核对哈希。

普通/unit 全量为 11,986 / 20,020 条通过；integration 本次 12,980 通过、5 跳过、7 失败事件，其中六个场景在 Docker 容器启动阶段超时，未进入业务断言，另一个为父测试失败。原场景保持断言与超时，串行补验 8 条通过；构建与三套 lint 为零，不将补验记作整组 integration 通过。定向 race 899 条通过，后续路由/固定状态合同 10 条通过，依赖门禁 30/30 通过。详情见归档中的 `progress-20260923-textattempt-verification.json`。

## 2026-09-23 旧通用 Handler 退役检查点

`progress-20260923-generic-retirement-manifest.json` 接续 textattempt 检查点，新增分卷经逐成员哈希核验。该批删除无生产消费者的旧通用 Handler，相关普通测试 1,480 条、定向 unit race 297 条通过；Wire、构建和三套完整 lint 均退出零。原夹具 typed-nil 迁移回归及修正日志均保留，验证索引为归档中的 `progress-20260923-generic-retirement-verification.json`。最终 S16 全量验收仍待完成；此前 integration 环境失败与补验边界不变。

## 2026-09-23 OpenAI 入站提示检查点

`progress-20260923-openai-hints-manifest.json` 接续 generic-retirement 检查点。定向 unit race 516 条通过、1 项既有跳过；普通合同 37 条通过，构建及三套完整 lint 为零。阶段仍实施中；命令与范围见归档中的 `progress-20260923-openai-hints-verification.json`。

## 2026-09-23 OpenAI 共用输出检查点

`progress-20260923-openai-output-manifest.json` 接续 openai-hints 检查点。定向 race 573 条、相关普通合同 263 条通过；构建及三套完整 lint 为零。11 组原断言迁入 HTTP 所有者，原诊断与修正记录保留。完整结果见归档内 `progress-20260923-openai-output-verification.json`；S16 尚未完成。

## 2026-09-23 OpenAI HTTP 资源检查点

`progress-20260923-openai-controls-manifest.json` 接续 openai-output 检查点。普通合同 48 条、定向 race 137 条通过；Wire 再生成稳定，构建和三套完整 lint 为零。唯一 limiter/并发资源的容量合同及原始诊断完整保留。结果见归档内 `progress-20260923-openai-controls-verification.json`，S16 仍未完成。

## 2026-09-23 Cyber 会话与装配检查点

`progress-20260923-cyber-manifest.json` 接续 openai-controls 检查点。合并普通/unit/integration 全量通过 11,987 / 20,021 / 12,990，既有跳过 4/8/4；三套完整 lint 为零。定向 unit race 48 条、真实 Redis integration race 1 条和边界门禁 27/27 通过；Wire 稳定性已验证。完整命令与事件见归档内 `progress-20260923-cyber-verification.json`。S16 未完成。

## 2026-09-23 OpenAI 文本 HTTP 检查点

`progress-20260923-openai-text-manifest.json` 接续 cyber 检查点。普通定向 674 条、unit race 1,132 条通过，各保留一项既有 WS 跳过；组合根/路由追加 race 42 条通过，三套完整 lint 为零。门禁 27/27，Wire 再生成稳定；失败及补验独立保留。完整结果见归档内 `progress-20260923-openai-text-verification.json`。S16 未完成。

## 2026-09-23 OpenAI 单次尝试运行时检查点

`progress-20260923-openai-attempt-manifest.json` 接续 openai-text 检查点。共享选择/反馈、槽位、失败输出与完成快照保持唯一；生产文本运行时不再经过旧 Handler。全部命令、真实测试事件、既有跳过和初次 unused 诊断保存在归档内 `progress-20260923-openai-attempt-verification.json`。依赖夹具 33/33 通过，Wire 稳定；S16 未完成。

## 2026-09-23 WS 入站检查点

`progress-20260923-ws-entry-manifest.json` 接续 openai-attempt 检查点。定向普通 644、unit race 818 条通过，各保留一项既有 WS 跳过；租约单测 race 10 条、真实 Redis integration race 10 条通过。三套完整 lint 为零，门禁 36/36、Wire 稳定；初次 unused 诊断独立保留。完整索引为 `progress-20260923-ws-entry-verification.json`。S16 未完成。

## 2026-09-24 媒体/辅助入口检查点

`progress-20260924-media-entry-manifest.json` 接续 20260923-ws-entry 检查点。生产 Wire/server 三套依赖图已无旧 handler。全仓普通/unit/integration 最终 12,006 / 20,040 / 13,009 条通过，既有跳过 4/8/4；三套 lint 为零。定向 race 1,238 条、真实 Redis/PG race 4 条通过；门禁 42/42、Wire 稳定。初次配置缩进、unused 及静态合同旧路径失败独立保存；完整索引为 `progress-20260924-media-entry-verification.json`。S16 未完成。

## 2026-09-24 旧 handler 清零检查点

`progress-20260924-handler-retirement-manifest.json` 接续 media-entry 检查点。143 项原测试/benchmark 声明和标签均有替代位置，未运行 benchmark。定向普通 2,021、unit race 1,569 条通过；完整普通/unit/integration 12,006 / 20,040 / 13,009，既有跳过 4/8/4，三套 lint 为零。门禁 36/36、Wire 稳定、八类文件选择已记录。完整索引为 `progress-20260924-handler-retirement-verification.json`。旧 handler 删除，S16 仍未完成。

## 2026-09-24 选择、评分与粘性能力退出

[本批验证索引](service-selection-owner-verification.json)与[增量清单](progress-20260924-selection-manifest.json)接续 model-read 检查点。新增分卷仅包含本批新增或变化证据，已逐成员读取并验证 SHA-256；按父链顺序恢复后，验证命令、305 个测试的来源/目标/断言对应、原失败日志和修正结果均可核对。

受影响范围普通/unit/integration 分别为 5,923 / 8,799 / 6,054 条通过事件，各保留 2 项既有跳过；三套 lint 均为零。定向 owner、混合执行、app 与真实 Redis race 分别通过 1,302 / 5 / 237 / 64 条事件，middleware Fast 合同补验通过。99 项门禁与 8 套构建选择核对通过，Wire 再生稳定。各集合存在重叠，不相加；本批不替代 S16 最终全仓验收。
