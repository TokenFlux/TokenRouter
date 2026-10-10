# TokenRouter 协作规范

## 开始任务

处理本仓库任务时，优先使用项目内置的 [project-doc](.agents/skills/project-doc/SKILL.md) 技能。

- 执行任何仓库任务前，先确认当前 Agent 会话能调用名为 `project-doc` 的技能。Claude Code 直接读取 `.agents/skills` 下的技能文件。
- 无法调用时立即停止，此后唯一允许的动作是回复：“当前环境未安装或未加载 `project-doc` 技能，按仓库规则无法继续。请安装或启用该技能，并在新会话中重试。”
- 能够调用时，先使用 `project-doc`。当前会话第一次处理本仓库任务时，完整读取 `docs/index.md`，再按目录路由完成任务。
- 同一会话里已经读过、上下文中仍保留足够内容的技能说明、索引和相关章节，直接复用，修改文件、继续子任务和任务收尾时也一样。内容缺失、相关文件变化或路由冲突时，按技能的“读取与上下文复用”规则补读受影响的部分。

## 编码与写作

- 每次编写或修改代码前，必须先加载并遵循项目级 [code-complete](.agents/skills/code-complete/SKILL.md) Skill，指导具体编码实现。执行前主动参考相关模块的现有实现，保持项目设计与编码风格一致。
- 涉及方法抽象、接口设计、模块边界或职责划分等设计决策时，额外加载并遵循项目级 [a-philosophy-of-software-design](.agents/skills/a-philosophy-of-software-design/SKILL.md) Skill。两者同时适用时，由后者指导设计与抽象决策，前者指导具体实现。
- 两个 Skill 默认使用各自的 mini 规范，必要时再查阅完整版，文件入口见各自的 `SKILL.md`。
- 编写或修改文字前，加载并遵循项目级 [humanizer](.agents/skills/humanizer/SKILL.md)，优先使用本仓库版本。适用范围包括代码注释、文档、提交信息、PR 描述、界面文案、i18n、错误与日志消息、计划、技能说明和 Agent 规范。首次使用时完整读取，内容遗失时补读。
- 文字表达冲突时，以 Humanizer 的规则为准。提交、交付文档或结束任务前，完成技能要求的“交付前自查”。

## 修改约定

- 所有任务直接在当前 `main` 分支上完成。用户明确要求时，才创建或切换 Git 分支。
- 手写代码按需添加中文注释，说明约束、失败行为和代码中看不出的原因。Go 文档注释遵循 Go 规范。
- 代码按可读性换行，一行写一件事。修改代码时同步更新注释，删除代码时一起清理失效的注释和空行。
- 后端 Go 代码的 import 在包名冲突等确有需要时才加别名。Go 文件直接从 `package` 声明或 `//go:build` 约束开始。
- 新增、拆分、合并、改名后端 Go 文件，或调整文件内声明顺序前，按 Project Doc 规则读取或复用[后端文件组织](docs/operations/development_workflow.md#backend_file_layout)，遵循其中的命名、测试归属、包说明和声明顺序要求。改名或合并文件时，同步更新引用这些文件名的文档、架构许可和注释。

## 验证与交付

- 开发时运行改动所在包或组件的局部测试。验证范围和命令按[验证策略](docs/operations/development_workflow.md#验证策略)执行。
- 每个本地检出执行一次 `make hooks`。推送时 hook 在当前工作区运行和 `make check` 相同的快检，按相对远端的改动选择检查项。
- 提交前检查本次变更中的矛盾和可安全清理的问题，一并处理与本次修改相关的内容。
- 每次提交前，在仓库根目录运行 `make fmt`，检查 diff，并手动暂存属于本次提交的修改。部分暂存的文件逐块确认。随后运行 `make fmt-check`，确认声明顺序和格式差异已经清零。工具行为、生成文件识别和已提交代码的检查方式见 [Go 格式化](docs/operations/development_workflow.md#go-格式化)。
- 提交信息遵循 Conventional Commits 规范。
- 推送后查看 CI 结果。CI 运行全量 lint、单元、集成、前端、构建和脚本检查，集成测试和改动包的下游使用方由 CI 检查。
- 本地需要完整验收时运行 `make verify`，它执行和 CI 相同的检查，需要 Docker。安全扫描使用 `make security`。
- 测试失败或检查中断时，修复原因后重新验证。跳过的测试说明原因，并标记为未验证。

## 计划模式

- 使用 Codex 计划模式时，开始实施前把定稿计划原样保存到 `.agents/plans/`。执行期间把任务进度追加到计划文件末尾。计划文件留在本地，提交时排除它们。
