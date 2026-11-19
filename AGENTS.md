# TokenRouter Collaboration Guidelines

## Starting a task

Use the repository's [project-doc](.agents/skills/project-doc/SKILL.md) skill first when working on this repository.

- Before any repository task, confirm that the current agent session can invoke a skill named `project-doc`. Claude Code reads skill files directly from `.agents/skills`.
- If the skill is unavailable, stop immediately. The only permitted next action is to reply: "The current environment has not installed or loaded the `project-doc` skill, so repository rules prevent further work. Please install or enable the skill and retry in a new session."
- When the skill is available, use `project-doc` first. On the session's first task in this repository, read all of `docs/index.md`, then follow its routing for the task.
- Reuse skill instructions, indexes, and sections already read in the same session while enough of their content remains in context. This also applies when editing files, continuing subtasks, and finishing work. If content is missing, relevant files change, or routing conflicts arise, reread the affected material under the skill's "Reading and context reuse" rules.

## Coding and writing

- Before writing or changing code, load and follow the project [code-complete](.agents/skills/code-complete/SKILL.md) skill for implementation guidance. First inspect existing implementations in the relevant module and follow the project's design and coding style.
- For decisions about method abstractions, interface design, module boundaries, or responsibilities, also load and follow the project [a-philosophy-of-software-design](.agents/skills/a-philosophy-of-software-design/SKILL.md) skill. When both skills apply, use this skill for design and abstraction decisions and `code-complete` for implementation.
- Read the `code-complete` and `a-philosophy-of-software-design` skills' `SKILL.md` files in full and apply their rules.
- Before writing, changing, or reviewing handwritten code, load and follow the project [code-comments](.agents/skills/code-comments/SKILL.md) skill, including changes that initially contain no comments. Meet its mandatory coverage, length, placement, and accuracy requirements for the task's scope.
- Before writing or changing other prose, load and follow the project [humanizer](.agents/skills/humanizer/SKILL.md) skill. This includes documentation, commit messages, PR descriptions, UI text, i18n entries, error and log messages, plans, skill instructions, and agent guidelines.
- Choose the writing skill by content type. Apply one of `code-comments` or `humanizer` to each passage. When a task includes both comments and other prose, use the corresponding skill for each. This division takes precedence over conflicting scope statements in the skills.
- Read each applicable skill in full on first use, and reread it if its content is lost. Before committing, delivering documentation, or finishing the task, perform the corresponding skill's self-review.
- Keep Project Doc concise, scannable, and decoupled from internal plumbing:
  - Lead each section with a summary table, flow diagram, or numbered rule list when describing stages, options, or more than two parallel cases. Keep prose paragraphs short (roughly 3-4 sentences) and avoid chaining unrelated rules with semicolons.
  - Record cross-module invariants, ordering constraints, failure and fallback semantics, external contracts, and troubleshooting entry points. Do not transcribe `app` wiring, Wire bindings, private adapter struct names, or single-function implementation details into prose.
  - Describe current behavior directly. Avoid PR-diff narration ("no longer...", "changed from...") in current-state documents; keep migration steps in `docs/operations/upgrade_notes.md`.
  - Keep one source of truth per rule or volatile version constant (such as cache versions or scheduler key prefixes), and link to that section from other documents instead of repeating it.
  - Run `make check-docs` after adding, moving, or reorganizing documentation or `@project-doc` anchors.

## Change conventions

- Work directly on the current `main` branch. Create or switch branches only when the user explicitly requests it.
- Use the project's designated brand-color token wherever the UI calls for brand color. Identify the correct token from the user-approved reference component and its theme and interaction state. Do not substitute another color or shade, darken or lighten it, or reduce its opacity for readability, contrast, aesthetics, skill recommendations, or other reasons unless the user explicitly requests that color change. This rule applies when following `oil-ui-pro` and all other UI skills. Resolve readability issues through layout, typography, or surrounding surfaces while preserving the brand color.
- Write code comments and docstrings in English. Cover the files, declarations, fields, tests, and operations required by `code-comments`, including private declarations. Follow Go documentation conventions and the repository's file-overview and `doc.go` placement rules.
- Wrap code for readability and put one operation on each line.
- Treat comment maintenance as part of every code change. When behavior, names, parameters, return values, constraints, or execution order change, update affected comments in the same edit. Check file overviews, declaration comments, field comments, and inline comments against the resulting implementation.
- When deleting or renaming a symbol or changing its responsibility, search affected modules, callers, and tests for the old name and keywords describing the previous behavior. Review each matching comment and documentation reference, and update or remove obsolete content based on the current implementation.
- When moving or extracting code, move its comments with it and revise them for the destination. Remove comments left at the source when they no longer describe the code there. When deleting or replacing code, remove obsolete explanations and duplicate comments.
- Keep comments next to the declarations or operations they explain. After editing, remove empty comment blocks and blank lines left by deleted or moved code or comments. Retain blank lines that separate meaningful logical groups and follow the language's formatting conventions.
- Add aliases to backend Go imports only when needed, such as to resolve package-name conflicts. Start Go files with the `package` declaration or a `//go:build` constraint.
- Before adding, splitting, merging, or renaming backend Go files, or changing declaration order, read or reuse [Backend file organization](docs/operations/development_workflow.md#backend_file_layout) under the Project Doc rules. Follow its naming, test ownership, package documentation, and declaration-order requirements. When renaming or merging files, update documentation, architecture allowances, and comments that reference those filenames.

## GitHub issues and pull requests

- Write authored issue and PR text in English without emoji. Preserve verbatim user quotations in their original language and form.
- Before an agent opens an issue, read and fill [.agents/github/ISSUE.md](.agents/github/ISSUE.md) as the entire issue body. Human contributors use the GitHub issue forms. Check the template's support boundaries, search the linked guides and engineering documentation, README, and relevant code, and answer usage, configuration, or integration questions without opening an issue.
- Quote the user's request and later corrections faithfully in the agent template, preserving the original language and line breaks. Do not rewrite or summarize those quotations. Keep the other answers short and factual, and do not paste unfiltered AI-generated text in the body or later comments.
- If required issue facts are missing, ask for the facts and wait. Do not invent actual behavior, impact, frequency, evidence attributing the problem to TokenRouter, or applicable relay, billing, frontend, and deployment details. Do not ask the user merely to confirm a template. Do not open an issue or PR while its required conditions are unmet.
- Before an agent opens a PR, compare `git config user.name` and `git config user.email` with the repository's historical core developers in `git log`. Do not change Git configuration. For contributors outside that group, explicitly state in the PR body that the code was AI-generated or AI-assisted.
- For a PR created for the project owner, use [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md). Do not use the agent PR template for the project owner unless the owner explicitly requests it.
- For other agent-created PRs, read and fill [.agents/github/PR.md](.agents/github/PR.md) as the entire body, unless the project owner explicitly requests the human template. Record commands or steps actually performed and the observed results; a claim that `go build` or tests passed is insufficient. Follow the template's issue-linking, scope, research, and verification requirements before opening the PR.

## Verification and delivery

- During development, run local tests for the changed package or component. Select scope and commands under the [Verification strategy](docs/operations/development_workflow.md#验证策略).
- Run `make hooks` once for each local checkout. On push, the hook runs the same quick checks as `make check` in the current working tree, selecting checks from changes relative to the remote.
- Before committing, inspect the task's changes for contradictions and safe cleanup opportunities, and address issues related to the change.
- Before committing or finishing a code task, read the resulting code in each changed file with its surrounding comments and spacing. Check comments outside the diff when the change affects their claims. Resolve stale descriptions, misplaced or orphaned comments, and unexplained blank lines before delivery; formatter and test success do not replace this review.
- Before every commit, run `make fmt` from the repository root, inspect the diff, and manually stage the changes belonging to that commit. Review partially staged files hunk by hunk. Then run `make fmt-check` and confirm that declaration-order and formatting differences are resolved. See [Go formatting](docs/operations/development_workflow.md#go-格式化) for tool behavior, generated-file detection, and checks of committed code.
- Write commit message subjects and bodies in English and follow Conventional Commits.
- After pushing, check CI results. CI runs full lint, unit, integration, frontend, build, and script checks. CI also checks integration tests and downstream users of changed packages.
- For full local acceptance, run `make verify`. It runs the same checks as CI and requires Docker. Run `make security` for security scans.
- If a test fails or a check is interrupted, fix the cause and rerun verification. Explain skipped tests and mark their scope as unverified.

## Plan mode

- When using Codex plan mode, save the finalized plan verbatim under `.agents/plans/` before implementation. Append progress to the end of that file during execution. Keep plan files local and exclude them from commits.
