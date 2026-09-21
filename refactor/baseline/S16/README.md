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
