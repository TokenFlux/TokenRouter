---
name: tr-release
description: Release TokenRouter at a requested version, or the next patch tag when no version is specified, with structured Chinese release notes. Use when the user invokes $tr-release or asks to publish a TokenRouter version, increment the latest vMAJOR.MINOR.PATCH tag, populate the GitHub Release changelog, or verify the release workflow.
---

# TR Release

## 目标与范围

为 TokenRouter 发布指定版本；未指定时递增 patch。发布说明使用中文，准确覆盖上一版本到目标提交的全部对外变化。workflow 把 annotated tag 的正文交给 GoReleaser，生成 Release description。

用户要求修订技能或审查说明时，只完成相应修改或审查；发布新版本和修改已有 Release 分别以用户请求为准。

## 确定仓库与版本范围

1. 在 TokenRouter 仓库根目录遵守仓库指令，先走 `project-doc` 发布路由，写说明时使用 `humanizer`。
2. 用 `git remote get-url origin` 确认发布目标，并从 URL 确定 `release_repo`（当前为 `TokenFlux/TokenRouter`）。所有 `gh` 命令都显式传入 `--repo "$release_repo"`；fork 的 `gh` 默认仓库可能是 upstream。若 origin 不是用户要求的目标，先核实再继续。
3. 运行准备命令。它会 fetch 和检查 origin 标签，但不创建或推送标签：

```bash
python3 .agents/skills/tr-release/scripts/tr_release.py --prepare
# 指定目标版本时：
python3 .agents/skills/tr-release/scripts/tr_release.py --prepare --version 0.2.0
```

脚本只从 origin 选择最高的 `vMAJOR.MINOR.PATCH`，忽略只存在于本地或 upstream 的标签。显式版本必须更高，`0.2.0` 和 `v0.2.0` 均可。保存输出的 `previous_tag`、`previous_commit`、`new_tag`、`commit`、`commit_count` 和固定 SHA 的 `change_range`。

4. 在目标仓库核对 `previous_tag` 对应的已发布 Release。标签没有 Release、Release 尚未成功，或与该版本线实际上一版不一致时，先查明原因，不能把未经核实的区间写成“本次更新”。脚本已要求基准是目标提交的祖先。已有 Release 的说明只能作为背景，不能替代本次代码证据。
5. 编写说明前，完整执行 [变更覆盖与事实核对](references/release-notes.md)。以固定 SHA 范围建立覆盖清单，检查每个提交和最终文件差异；每项变化都要落到发布条目或有依据的排除原因。输出截断时分批读取，不能用抽样代替全量检查。

## 编写与审查说明

先完成覆盖清单，再整理为下列结构，仅保留有内容的分类。存在配置删除、API 不兼容、计费变化或迁移要求时，把有证据支持的 `## 升级注意` 放在功能分类前面。

```markdown
一句话概括本次最重要的用户影响、风险或升级理由。

## 新增功能

- 说明能力、适用对象和结果

## 优化改进

- 说明优化前的问题和优化后的效果

## Bug 修复

- 说明故障场景、影响和修复结果
```

按能力和使用场景组织条目；同一能力的连续修正可以合并，独立能力、故障和升级影响必须保留。完整性优先于篇幅，不设条目数量上限，也不能把多个独立变化压缩成“优化界面”“提升稳定性”。新增、变更、修复的分类依据上一版本的实际行为。

只写代码与测试支持的适用范围、默认值和效果。禁止把重构直接写成性能提升，把新增测试写成修复，或从新增配置推断默认启用。不确定的事实继续核查；不能编造，也不能把尚未核查的用户影响当作内部变化删掉。安全结论、停机要求和回滚限制同样需要证据。

不重复 Release 标题、产品引言、通用安装说明或文档链接，这些由 GoReleaser 提供。提交 SHA 和内部覆盖清单不进入 Release 正文。确认无对外影响的测试、机械重构、格式化和开发工具变更可以排除；不能按提交前缀或目录直接排除。

按参考文档完成双向核对：每项对外变化能找到条目，每条描述能回到固定目标提交上的证据。保存最终说明为 UTF-8 文件，发布时使用同一文件，避免重新摘要时丢失细节。发布前向用户简要展示版本范围和说明；已有发布授权时继续执行，无须额外请求确认。

## 发布与验收

发布时传入准备阶段记录的完整 SHA 和上一标签；如果远端新发布或本地 HEAD 变化，脚本会拒绝旧范围，需要重新核对说明。示例中的变量取自准备输出，`notes_file` 指向已审查的说明文件：

```bash
python3 .agents/skills/tr-release/scripts/tr_release.py \
  --expected-head "$release_commit" \
  --expected-previous-tag "$previous_tag" \
  --notes-file "$notes_file"
```

用户指定版本时，在同一命令中加上 `--version VERSION`；否则保持自动递增。仍可用 `--notes-file -` 从标准输入传入完整说明。

1. 在明确的目标仓库等待 `new_tag` 触发的 `Release` Actions 成功，核对 tag 和提交，避免把相同提交上的其他运行当作本次发布。
2. 用 `gh release view "$new_tag" --repo "$release_repo" --json body,url,tagName` 读取最终正文。对比完整说明区块，只允许换行符和区块首尾空白差异；摘要、每个分类和每条列表内容都必须保留。标题存在或正文非空不足以证明验收通过。
3. 若 workflow 成功但正文缺失、被截断或变更条目不同，保留生成的引言和安装/文档页脚，用已审查说明文件替换变更说明区块，再通过 `gh release edit "$new_tag" --repo "$release_repo" --notes-file ...` 修正并重新读取。无法识别区块边界、出现他人修改或修正后仍不一致时报告具体问题，停止反复覆盖。不要移动现有 tag。
4. 完成仓库发布路由要求的其他检查；未执行或失败的检查应如实报告。返回上一标签、新标签、提交 SHA、Actions 结果、Release 链接及完整正文比对结果。

## 脚本边界

- 发布前要求已跟踪文件无暂存或未暂存改动；未跟踪文件不进入发布提交。有 upstream 时，当前分支必须与其对齐。
- 在已核对的提交上创建 annotated tag，标题为 `TokenRouter MAJOR.MINOR.PATCH`，正文逐字保留说明后推送到 origin。
- 拒绝覆盖本地或远端标签。注解校验或推送失败时删除本次创建的本地标签。
- 脚本仅检查说明的基本 Markdown 结构，不会验证语义准确性或变更覆盖率；这些由发布前的证据清单和双向审查完成。
- 用户未要求时不创建 release commit，不移动已有标签。推送成功不代表发布完成。
