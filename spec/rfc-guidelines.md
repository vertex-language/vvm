# RFC Guidelines — Changing the Vertex IR Spec

This document is the bar a proposed change to `README.md` (the base spec) has to clear
before it's accepted. It exists because the spec's value comes from what it *refuses*
to include, not just what it contains. Every accepted addition is permanent surface
area — every consumer (`ir/verify`, `importer`, every `lower/<arch>` backend, every
frontend targeting Vertex IR) has to carry it forever. Treat additions as expensive by
default.

---

## 0. The one outright rejection

**Inline or native assembly of any form is not eligible for re-inclusion in `.vir`/`.vbyte`,
full stop.** Not as an opcode, not as a block form, not as an escape hatch, not behind a
feature tier. This was tried, removed, and the removal is final — see `asm.md`. An RFC
proposing inline asm, an "asm-lite," a dialect-tunneling opcode, or any grammar that
embeds raw target-specific mnemonic text inside a `.vir` module will be rejected without
further discussion. If the need is "call into hand-written machine code," the answer is
always: write it as `.ss`, compile to `.o`, link it in. That path is deliberately the only
path. Do not propose narrowing it.

---

## 1. Default posture: reject

Every RFC starts from "no." The spec's design principles (§1 of `README.md` — CPU-only,
no runtime, strict semantics, one behavior per opcode, minimal UB) are the filter every
proposal passes through, not background flavor text. If a proposal weakens any of them
to accommodate a use case, that's a strong signal the use case belongs in a frontend, a
library, or a separate package — not in the IR.

## 2. What every RFC must answer, in order

An RFC that doesn't answer these, in this order, gets sent back before technical review starts.

1. **What can't be expressed today?** Show real `.vir` that a real compiler backend would
   want to emit, and show where it's currently impossible or requires a bad workaround.
   "Would be more convenient" is not "can't be expressed." If existing opcodes plus
   ordinary function composition can express it, that's the answer — not a new opcode.
2. **Why is this an opcode/grammar problem, not a library/frontend problem?** Vertex IR
   is deliberately low-level and un-clever. Sugar, convenience wrappers, and
   higher-level constructs belong in whatever compiles down *to* `.vir`, not in `.vir`
   itself. Ask: could this be a function in an `extern`-callable support library instead?
   If yes, that wins by default.
3. **What's the minimal grammar/semantics that solves it?** Propose the smallest possible
   surface — one opcode, one attribute, one restriction — not a family of variants "while
   we're at it." A proposal that solves the stated problem plus three adjacent problems
   nobody asked about should be split or trimmed.
4. **What does it cost?**
   - New verification rules `ir/verify` must enforce, and whether they're checkable
     locally (single-pass, single-module) or need new global state.
   - New cases every `lower/<arch>` backend must handle, and whether any backend
     *can't* reasonably support it (if one target can't, that's a serious strike against
     a CPU-only, target-agnostic IR).
   - New UB or trap surface (does it add an 11th way to trigger UB? justify it against
     §5.4's "exactly 10" framing — that number is a design constraint, not a count that
     happens to be true today).
   - Whether it interacts with the Join Convention, `importer`'s cross-module rewriting,
     or ABI/mangling rules in a way that needs its own subsection.
5. **What did you reject, and why?** List at least one simpler or more general
   alternative you considered and didn't propose. An RFC with no rejected alternatives
   usually means insufficient search, not that the proposal was the only option.

## 3. Bias checklist (answer honestly, in the RFC)

- Does this proposal make any *existing* opcode's semantics conditional on target,
  flag, or mode? If yes, it likely violates "one behavior per opcode" and needs
  exceptional justification.
- Does it require a frontend to know something about a specific CPU to use it
  correctly? If yes, it's leaking target-dependence into a place meant to be
  target-independent, and should probably be backend-only (a `lower/<arch>` concern,
  invisible at the `.vir` level) instead of IR-visible.
- Could this be solved by composing existing opcodes, even if less efficient, and
  leaving the efficient path to `lower/<arch>`'s optimizer? IR-level "performance"
  arguments for new opcodes are usually backend arguments in disguise.
- Is there a real, named backend/target that cannot implement this? A single
  counterexample target is often enough to reject a "hardware mapping" claim.

## 4. What good proposals look like

Good: "`va_arg`'s trap-on-overread is UB, not a trap, because backends can't check it
cheaply and cheap-everywhere is a hard requirement" — ties a specific design constraint
to a specific rejected alternative (making it a trap).

Bad: "Add a `fast_div` opcode for platforms with fast division" — reintroduces
target-dependent semantics under a new name and duplicates what feature tiers already
express; almost certainly rejected under §1's strict-semantics stance, same category
of mistake as inline asm.

## 5. Format

Submit as a single markdown file per proposal, sections in the order of §2 above.
Diffs to `README.md` are written last, only after §2's five questions are answered —
a proposal that opens with a diff instead of a justification will be read as "I already
decided" and reviewed more skeptically, not less.