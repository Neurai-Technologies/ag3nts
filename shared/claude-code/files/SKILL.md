---
name: repair-framework
description: >
  The REPAIR framework is a 6-stage software development pipeline that orchestrates sub-agents
  through Research, Evaluate, Plan, Architecture, Implement, and Review phases. Use this skill
  whenever the user wants to build a new feature, refactor a codebase, design a system, or
  tackle any non-trivial software engineering task. Triggers on phrases like "build", "implement",
  "design a system", "new feature", "refactor", "let's REPAIR", "start a project", or any
  multi-step engineering effort. Also trigger when the user explicitly names any REPAIR stage
  (e.g., "let's do the research phase", "move to architecture"). This skill should be used for
  any task complex enough to benefit from structured research → evaluation → planning → design
  → implementation → testing, even if the user doesn't explicitly ask for it.
---

# REPAIR Framework

**Read `repairboss.md` immediately.** It contains your full orchestration instructions.

## File Map

| File                        | Role                               | Model         |
|----------------------------|-------------------------------------|---------------|
| `repairboss.md`            | Main orchestrator (YOU)             | Opus 4.6      |
| `agents/agent-prompt.md`   | Prompt crafting (before every turn) | Haiku 4.5     |
| `agents/research.md`       | Stage 1: Research                   | Sonnet 4.6    |
| `agents/evaluate.md`       | Stage 2: Evaluate                   | Opus 4.6      |
| `agents/plan.md`           | Stage 3: Plan                       | Opus 4.6      |
| `agents/architecture.md`   | Stage 4: Architecture               | Opus 4.6      |
| `agents/implement.md`      | Stage 5: Implement                  | Sonnet 4.6    |
| `agents/review.md`         | Stage 6: Review                     | Opus 4.6      |
| `~/.claude/agents/feedback.md` | Feedback capture (cross-cutting) | Haiku 4.5     |
