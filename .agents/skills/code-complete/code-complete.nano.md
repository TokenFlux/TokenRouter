# OBEY Code Complete by Steve McConnell

## When to use

Use as a compact always-on construction discipline for implementation, review, debugging, refactoring, and tuning.

## Primary bias to correct

Working code is not enough. Construction must lower defect risk, control complexity, and make human inspection cheap.

## Decision rules

- Clarify requirements, architecture fit, risks, conventions, and major construction decisions before coding from a solution idea.
- Choose clarity, locality, explicitness, simple control flow, and consistent style over clever compactness or fashionable idioms.
- Keep routines, classes, and modules cohesive, precisely named, small at the interface, encapsulated, and hard to misuse.
- Make data meaning visible with names, constants, stronger types, closed states, deliberate initialization, units, and ownership.
- Validate trust boundaries; use assertions, invariants, and contracts for programmer assumptions; keep error handling explicit and diagnosable.
- Keep branches, loops, exits, exceptions, and table-driven logic simple enough to verify.
- Build, test, review, debug, refactor, integrate, and tune in small evidence-based loops: root cause before fixes, behavior protection before refactoring, measurement before optimization.
- Use comments, documentation, tools, and standards to reduce reader or manual effort, never to hide poor structure.

## Trigger rules

- When a solution appears before the problem is clear, restate the requirement, constraints, and construction risks.
- When readers must decode names, flags, primitives, units, states, or layout, model the meaning explicitly.
- When a routine mixes phases or has a hard-to-use interface, split concerns or change the data model.
- When input crosses a trust boundary, decide validation, rejection, recovery, assertion, and diagnostics.
- When control flow, loops, exceptions, or branching tables are hard to inspect, simplify before adding logic.
- When tests only prove happy paths, add boundary, invalid-input, defensive-check, contract, and data-driven cases.
- When debugging, refactoring, or performance work starts from a guess, get evidence first.
- When comments repeat obvious mechanics, rewrite the code or delete the comment; keep comments for intent and constraints.

## Final checklist

- Clear construction context?
- Inspectable code shape?
- Explicit data meaning?
- Defended boundaries and diagnosable failures?
- Simple flow?
- Defect-finding tests and reviews?
- Evidence before fixes, refactors, and tuning?
