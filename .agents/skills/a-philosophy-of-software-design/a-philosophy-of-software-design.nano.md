# OBEY A Philosophy of Software Design by John Ousterhout

## When to use

Use when the main risk is accidental complexity, shallow abstractions, leaky interfaces, or tactical patches.

## Primary bias to correct

Working code, small pieces, and familiar wrappers are not automatically simple.

## Decision rules

- Optimize for lower cognitive load and local understandability, not shorter files, familiar patterns, fewer lines, or clever compactness.
- Prefer deep modules; reject wrappers, layers, helpers, facades, and split-outs that do not hide real complexity.
- Hide volatile decisions, representations, storage, protocol facts, workflow bookkeeping, and messy edge handling in one owning module.
- Make interfaces caller-centered and semantic; avoid staged APIs, flags, setup sequences, and mechanism leakage when the module can provide the right operation.
- If a change feels awkward or spreads widely, improve ownership and abstraction instead of adding tactical special cases.
- Combine or split by total complexity: keep shared knowledge together and split only at independently understandable boundaries.
- Treat names and comments as design signals: precise abstraction names, explicit contracts, and no comments that compensate for bad decomposition.
- Add complexity for performance, trends, patterns, frameworks, tests, or exception handling only when evidence or caller needs justify it.

## Trigger rules

- When adding a helper, layer, option, callback, pattern, or abstraction, prove it removes complexity for callers.
- When an API requires sequencing, representation, storage, transport, caching, protocol, or file-format knowledge, redesign the boundary.
- When naming is hard, comments get long, or reviewers are surprised, treat it as design evidence.
- When one change spreads widely, look for duplicated knowledge, hidden dependencies, temporal coupling, or the wrong owner.
- When optimizing or adding exception paths, keep the common path simple and require evidence or a stronger invariant.

## Final checklist

- Fewer concepts to hold?
- More complexity hidden below the right boundary?
- Fewer special cases, knobs, leaks, and call-order traps?
- Better names, contracts, ownership, and evidence for any added complexity?
