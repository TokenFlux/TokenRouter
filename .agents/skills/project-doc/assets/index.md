# Project Doc 目录模板

<!-- 使用时只保留一个适用变体，替换全部占位符，并按仓库主语言改写。 -->

## 根目录变体

~~~markdown
# {{project_name}} 工程文档

> 本文件是 Project Doc 的根目录。当前会话加载 project-doc 技能后，首次处理本仓库任务时完整读取本文件，再按“读取时机”进入相关分类；后续按技能的“读取与上下文复用”规则复用已读内容，仅在内容缺失或需要刷新时补读。

## 文档范围

{{用一至两句话说明文档库覆盖的持久工程知识，以及明确排除的内容。}}

## 根级文档

- [{{document_title}}]({{document_path}})：{{one_sentence_summary}}。读取时机：{{read_when}}。

## 分类

- [架构](architecture/index.md)：{{architecture_summary}}。读取时机：{{architecture_read_when}}。
- [领域](domains/index.md)：{{domains_summary}}。读取时机：{{domains_read_when}}。
- [接口](interfaces/index.md)：{{interfaces_summary}}。读取时机：{{interfaces_read_when}}。
- [运维](operations/index.md)：{{operations_summary}}。读取时机：{{operations_read_when}}。
- [决策](decisions/index.md)：{{decisions_summary}}。读取时机：{{decisions_read_when}}。

<!-- 建库期间才保留以下内容；目标不存在时写作代码路径，不创建失效链接。 -->
## 建库状态

- [planned] {{planned_document_path}}：{{planned_scope}}。
- [in_progress] {{current_document_path}}：{{current_scope}}。
~~~

删除没有实际文档的分类，不创建空目录。建库完成后删除“建库状态”章节和所有状态前缀。

## 分类目录变体

~~~markdown
# {{category_name}} 文档目录

> 上级目录：[工程文档](../index.md)

## 范围

{{说明本分类拥有的知识边界，以及什么内容应路由到其他分类。}}

## 文档

- [{{document_title}}]({{document_file}})：{{one_sentence_summary}}。读取时机：{{read_when}}。

<!-- 建库期间可以使用 planned、in_progress、verified、blocked；完成后移除状态。 -->
~~~

每篇文档只在一个分类目录中作为规范条目出现。相关分类或相关文档之间使用普通链接，不复制概要的规范所有权。
