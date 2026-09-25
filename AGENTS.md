# TokenRouter 协作规范

## Project Doc 门禁

本仓库在 `.agents/skills/project-doc/` 内置 `project-doc` 技能，处理本仓库任务时优先使用此版本。磁盘上存在技能目录不代表当前 Agent 会话已经加载该技能。

- 执行任何仓库任务前，先确认当前 Agent 会话能够调用名为 `project-doc` 的技能。
- 如果不能调用，立即停止；不得读取业务文件、运行仓库命令、执行分析或修改文件。
- 技能不可用时，只回复：“当前环境未安装或未加载 `project-doc` 技能，按仓库规则无法继续。请安装或启用该技能，并在新会话中重试。”
- 如果能够调用，先使用 `project-doc`；当前会话首次处理本仓库任务时完整读取 `docs/index.md`，再按目录路由完成任务。
- 同一会话中已读取且仍保留足够内容的技能说明、索引和相关章节可以复用；修改文件、继续子任务或任务收尾本身不触发重新读取。内容缺失、相关文件变化或路由冲突时，按技能的“读取与上下文复用”规则补读受影响部分。

## 通用规范

- 代码必须包含注释，注释统一使用中文。
- 代码不要刻意压行，保持可读性。
- Go 代码 import 语句不允许添加不必要的别名。
- 写代码注释和文档需要使用 Humanizer 技能。
- Commit message 必须遵循 Conventional Commits 规范。
- 除非用户明确要求，否则不得创建或切换 Git 分支；所有任务直接在当前 `main` 分支上完成。

## Go 格式化

- 每次提交代码前，必须在仓库根目录运行 `make fmt-go-changed`，通过 `golangci-lint fmt` 对本次改动的手写 Go 文件执行格式化，排除自动生成的文件。
- 格式规则统一维护在 `backend/.golangci.yml`：启用 `gofumpt` 默认规则，保留现有 `gofmt` 重写规则。工具版本以 `.golangci-version` 为准，本地与 CI 必须一致，不单独安装或固定 `gofumpt` 版本。
- 命令覆盖暂存、未暂存及未跟踪的 Go 文件；生成文件按 `package` 声明前的 `// Code generated ... DO NOT EDIT.` 标记识别。不要直接对生成文件运行格式化。
- 格式化以整个改动文件为单位。执行后检查 diff，将属于本次提交的格式化结果重新暂存；部分暂存文件要逐块确认，命令不会自动 `git add`。
- 提交前运行 `make check-fmt-go-changed` 确认没有剩余格式差异；没有 Go 文件改动时命令会直接通过。
- 检查已提交代码时使用 `make check-fmt-go-changed FMT_BASE=<基准提交>`。CI 的 PR 基准为目标分支与源提交的共同祖先，push 基准为推送前的提交，不能用干净工作区的未提交差异代替。

## 计划模式

- 使用 Codex 计划模式时，开始实施前必须先将计划原样保存到 `.agents/plans/`，不允许对撰写好的计划进行修改或简化，确保执行期间可以随时回看；执行期间将任务进度同步到计划文件末尾中。这些计划不需要提交到git仓库

## 前端规范

- 需要选择框时，必须使用项目自研的选择框组件，不得使用原生 `<select>`。
- 圆角只用语义类名 `rounded-compact/control/surface/dialog`（另允许 `rounded-full/none`）；禁用 `rounded-sm/md/lg/xl` 等旧尺度名、裸 `rounded` 和 `rounded-[...]` 任意值。
- 字号下限 `text-xs`（12px），禁止 `text-[9px]/[10px]/[11px]` 等任意小字号。
- 修改前端样式后运行 `npm run check:ui` 校验上述规则，完整约定见 `docs/architecture/frontend_ui_conventions.md`。
