# OBEY A Philosophy of Software Design by John Ousterhout

## When to use

Use for module design, API changes, decomposition, refactoring, naming, comments, tests, performance work, and changes that feel awkward or spread complexity across files.

## Primary bias to correct

Working code, small pieces, familiar patterns, flags, wrappers, and extra documentation do not make a design simple when they increase cognitive load or leak knowledge.

## Decision rules

- Use reduced complexity as the primary success metric. Prefer the design that lowers cognitive load, change amplification, hidden dependencies, temporal coupling, and the number of facts a reader must hold at once.
- Treat design as continuous work. A first working patch is not done if it worsens future changeability; compare plausible alternatives for non-trivial interface, decomposition, or abstraction choices.
- Prefer deep modules: small, semantic interfaces that hide meaningful internal complexity. Reject pass-through services, thin library wrappers, helper modules, and tiny split-outs that add names without reducing reader burden.
- Design interfaces around what callers need to know, not how the implementation works. Avoid fragile staging, setup sequences, mode flags, configuration knobs, and arguments that expose internal choices.
- Hide volatile decisions, internal representations, storage shape, protocols, file formats, performance hacks, bookkeeping, normalization, and messy edge handling inside the module that owns the knowledge.
- Pull complexity downward when the lower module owns the detail. Prefer a slightly more complex implementation if it gives callers a simpler public contract and removes repeated reasoning from call sites.
- Choose generality at the right level. Avoid one-caller overfitting, vague speculative abstractions, and core paths polluted by rare edge cases; isolate special behavior with special-general decomposition.
- Combine or split by total complexity, not by size, runtime order, habit, or aesthetics. Keep related state, behavior, invariants, and design decisions together unless the new boundary is deeper and independently understandable.
- Reduce exception surface by changing interfaces or invariants where possible. Define away invalid states and awkward cases instead of making every caller repeat defensive ceremony.
- Use comments to reduce complexity: document interface contracts, invariants, hidden design decisions, rationale, and tricky implementation facts callers should not need to know. Do not narrate code or compensate for bad names, poor decomposition, or confusing flow.
- Treat names, consistency, and obviousness as design information. Names should reveal abstractions rather than mechanisms; related operations should share conventions; surprising code is complexity even when short.
- Use tests to protect behavior through public contracts and stable APIs, especially around hidden complexity and isolated special cases. Do not let test convenience force shallow or leaky interfaces.
- Add performance optimizations, trends, paradigms, patterns, or frameworks only when they reduce complexity in this codebase or evidence shows the tradeoff matters; hide optimization details behind stable interfaces.

## Trigger rules

- When a feature feels awkward, one change spreads across files, or reviewers must reconstruct hidden dependencies, look for missing information hiding, shallow modules, temporal coupling, or complexity pushed to callers.
- When adding a module, layer, service, helper, wrapper, facade, pattern, option, callback, or argument, prove that it hides more complexity than it adds.
- When touching an API, check whether ordinary callers must know sequencing, representation, storage, transport, caching, protocol, file format, internal workflow, or too many setup steps.
- When adding a special case, flag, exception path, conditional, or exposed container, first ask whether the owning module can eliminate the invalid state, isolate the unusual behavior, or provide a stronger operation.
- When splitting, extracting, or introducing variables, check whether the new boundary or name captures meaning or only adds jumps, pass-through state, and visible intermediate steps.
- When code is organized as `prepare/process/finalize`, staged objects, or other execution-order phases, verify that temporal structure is the real concept; otherwise reorganize around stable responsibilities.
- When naming is vague, mechanism-focused, inconsistent, or surprising, reconsider the abstraction boundary instead of accepting a near miss.
- When comments get long, duplicate code, justify a confusing interface, or explain usage by exposing internals, redesign the abstraction or move the missing contract to the interface.
- When optimizing performance, measure first and hide the optimization; do not sacrifice module depth or information hiding without evidence that the tradeoff matters.
- When testing or reviewing, focus on public behavior, interface contracts, hidden complexity through stable APIs, and special cases isolated behind the abstraction.

## Final checklist

- Did the change reduce the effort required to understand, modify, verify, and extend the system?
- Does every interface element, wrapper, layer, helper, option, and name hide enough complexity to justify its existence?
- Are important decisions localized, dependencies visible, caller-needed constraints documented, and mutable internals protected?
- Did common cases become automatic while rare controls, special cases, performance tricks, and exception details stayed out of the common path?
- Are names precise and consistent, comments current and non-duplicative, and conventions followed unless new information justified changing them?
