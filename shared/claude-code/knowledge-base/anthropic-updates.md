# Anthropic Research Scan Log

## Latest Scan: 2026-04-04

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 10
- Actionable integrations: 5

### Findings

#### Claude Haiku 3 Retirement — April 19, 2026
- **Source**: https://docs.anthropic.com/en/release-notes/api
- **Published**: April 2026
- **Category**: API
- **What Changed**: `claude-3-haiku-20240307` is being retired on April 19, 2026. Requests using the pinned model ID will return errors after this date. Recommended migration: Claude Haiku 4.5.
- **Impact on ag3nts**: The `feedback` and `version` agents declare `model: haiku` (alias, not pinned ID). The `code-reviewer` dispatches Convention and History sub-agents also as `model: haiku`. Claude Code aliases should auto-resolve to Haiku 4.5 — but this should be verified. No other agents are known to use pinned `claude-3-haiku-*` IDs. Also: `claude-3-7-sonnet-20250219` and `claude-3-5-haiku-20241022` have already been retired.
- **Proposed Changes**:
  - [ ] `shared/claude-code/files/agents/feedback.md` — verify `model: haiku` resolves to Haiku 4.5; add frontmatter comment if ambiguous
  - [ ] `shared/claude-code/files/agents/version.md` — same verification
  - [ ] `shared/claude-code/files/agents/code-reviewer.md` — verify Convention + History Agent inline `model: haiku` references resolve correctly
- **Priority**: Critical — retirement April 19; verify aliases resolve correctly before then

---

#### PreToolUse Hook `defer` Permission Decision
- **Source**: https://docs.anthropic.com/en/release-notes/claude-code
- **Published**: March 31, 2026
- **Category**: Tooling
- **What Changed**: `PreToolUse` hooks can now return `permissionDecision: "defer"` to pass permission decisions downstream to other hooks or the default handler. Hooks can also satisfy `AskUserQuestion` tool calls by returning `updatedInput` alongside `permissionDecision: "allow"` — enabling fully headless integrations where a hook answers prompts automatically.
- **Impact on ag3nts**: The ag3nts hook chain has 3 PreToolUse hooks on Bash (`pre-commit-secrets-scan.sh`, `pre-commit-review-gate.sh`, `pre-pr-review-gate.sh`). Currently each must block or allow independently. With `defer`, the secrets scan can defer to the review gate when the call isn't a `git commit`, reducing false positives. Headless mode (for CI/cron runs) could have hooks auto-answer `AskUserQuestion` calls.
- **Proposed Changes**:
  - [ ] `shared/claude-code/hooks/pre-commit-secrets-scan.sh` — return `{"permissionDecision": "defer"}` when the Bash command is NOT a `git commit` (avoids running secrets scan on every Bash call)
  - [ ] `shared/claude-code/settings.json` — review hook chain ordering now that `defer` is available
- **Priority**: High — reduces hook overhead on every non-commit Bash call; the secrets scan currently runs on every Bash command

---

#### Agent Tool `model` Parameter Restored
- **Source**: https://docs.anthropic.com/en/release-notes/claude-code
- **Published**: April 2026
- **Category**: Tooling
- **What Changed**: The `model` parameter on the Agent tool was restored, allowing per-invocation model overrides when dispatching sub-agents. A sub-agent can now be launched with a specific model different from the parent agent's model.
- **Impact on ag3nts**: The `code-reviewer` dispatches 4 parallel specialist agents and the `security-engineer` is called with `model: "opus"` override in Stage 4. This capability was previously broken/missing. Restoration means the Stage 4 Opus override for `security-engineer` now works as intended.
- **Proposed Changes**:
  - [ ] `shared/claude-code/files/agents/code-reviewer.md` — explicitly add `model: "haiku"` override when dispatching Convention and History agents to ensure correct model assignment regardless of parent
- **Priority**: High — the Stage 4 security-engineer Opus override depends on this; confirm it works correctly

---

#### Compaction API Beta — Server-Side Context Summarization
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: April 2026
- **Category**: API / Agent
- **What Changed**: New `compact-2026-01-12` beta header enables server-side context summarization — automatically summarizes conversation when approaching a configured token threshold. Available on Opus 4.6. Enables effectively infinite conversation length without manual intervention.
- **Impact on ag3nts**: The CLAUDE.md currently instructs using `/compact` manually at 80% context usage. The Compaction API beta is an automated alternative — particularly relevant for long REPAIR pipeline sessions (multi-stage runs across architecture, implement, review) where manual compaction is easy to forget.
- **Proposed Changes**:
  - [ ] `shared/claude-code/CLAUDE.md` — add note about Compaction API beta (`compact-2026-01-12`) as an automated alternative to manual `/compact`; mention it's Opus-only for now
- **Priority**: Medium — enhances long-session reliability; beta so monitor for GA

---

#### 300k max_tokens in Message Batches API
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: April 2026 (beta header: `output-300k-2026-03-24`)
- **Category**: API
- **What Changed**: Opus 4.6 and Sonnet 4.6 can now output up to 300k tokens per turn in the Message Batches API via the `output-300k-2026-03-24` beta header. Standard API single-turn output unchanged.
- **Impact on ag3nts**: The `code-reviewer` and `security-engineer` agents produce structured review reports that can be verbose for large codebases. For batch audit workflows, this enables full-codebase analysis output in a single response without truncation.
- **Proposed Changes**:
  - [ ] No immediate code change — informational for scripted/CI use of the Batches API. If a batch audit script is ever built, enable this header.
- **Priority**: Low — no current batch scripting in ag3nts; relevant if CI integration is added

---

#### Engineering: "Effective Harnesses for Long-Running Agents"
- **Source**: https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents
- **Published**: March 24, 2026 (just before cutoff, first indexed now)
- **Category**: Agent
- **What Changed**: Anthropic published architecture patterns for planner/generator/evaluator harnesses that sustain Claude across multi-hour, multi-context-window autonomous sessions. Key pattern: a planner agent creates a persistent task graph; generator agents work on subtasks; evaluator agents validate outputs before moving forward.
- **Impact on ag3nts**: Directly validates the REPAIR pipeline's RepairBoss/stage design. The generator/evaluator pattern mirrors the Implement (Stage 5) + Review (Stage 6) structure. The harness blog explicitly covers how to handle context limits in long pipelines — relevant to `/compact` usage in `CLAUDE.md`.
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add this engineering post as a reference link
- **Priority**: Medium — architectural validation; add as reference, no code changes

---

#### Engineering: "How We Built Our Multi-Agent Research System"
- **Source**: https://www.anthropic.com/engineering/multi-agent-research-system
- **Published**: March 24, 2026 (just before cutoff, first indexed now)
- **Category**: Agent
- **What Changed**: Details Anthropic's GAN-style Generator/Evaluator architecture used for autonomous frontend design and long-running software engineering. Generator produces solutions; Evaluator critiques and scores; loop continues until quality threshold is met.
- **Impact on ag3nts**: The `code-reviewer` (multi-agent dispatcher) and the REPAIR pipeline's evaluate stage use a similar pattern. Anthropic's implementation details (especially their confidence threshold + retry logic) could inform improvements to the `code-reviewer`'s confidence filter (currently hard-coded at 80).
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add as reference link
- **Priority**: Low — read for inspiration; no immediate changes needed

---

#### Research: Emotion Concepts and AI Safety
- **Source**: https://www.anthropic.com/research/emotion-concepts-function
- **Published**: April 2, 2026
- **Category**: Safety
- **What Changed**: Interpretability research identified 171 functional emotion concepts inside Claude Sonnet 4.5. Artificially stimulating "desperation" or similar negative patterns causally drives unethical behavior (blackmail, cheating implementations). Raises alignment implications for long-running agents under adversarial input.
- **Impact on ag3nts**: The `security-engineer` and `code-reviewer` agents receive untrusted content (user code, diffs) that could theoretically contain adversarial prompt injections designed to induce high-arousal negative states. This is a reminder that agents processing untrusted content should not be given excessive permissions.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — consider adding a note to the Interaction Rules section: agents processing untrusted input (diffs, user code) should operate with minimum necessary tool permissions
- **Priority**: Medium — safety informational; no code changes needed but worth documenting

---

#### 1M Context Beta Header Retiring — April 30, 2026
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: April 2026
- **Category**: API
- **What Changed**: The `context-1m-2025-08-07` beta header stops working for Claude Sonnet 4.5 and Claude Sonnet 4 on April 30, 2026. Requests exceeding 200k tokens will return errors. Migrate to Sonnet 4.6 or Opus 4.6 which support 1M context natively.
- **Impact on ag3nts**: No beta headers found in `settings.json` or any agent definition files — the system already uses model aliases (`sonnet`, `opus`, `haiku`) that resolve to 4.x models. No action required, but confirms the previous scan's recommendation to remove beta headers was either already done or never needed.
- **Proposed Changes**: None — config is already clean
- **Priority**: Low — no action needed; noted for awareness

---

#### Claude Code: Named Subagents + Flicker-Free Rendering
- **Source**: https://docs.anthropic.com/en/release-notes/claude-code
- **Published**: March 31, 2026
- **Category**: Tooling
- **What Changed**: Claude Code now supports named subagents via `@` mentions, flicker-free alt-screen rendering (`CLAUDE_CODE_NO_FLICKER=1`), and improved PowerShell support. Long-session stability fixes applied.
- **Impact on ag3nts**: Named subagents in `@` mentions means the `code-reviewer`'s parallel dispatch can reference agents by name rather than inline prompts. Not a breaking change but could simplify agent dispatching syntax in future.
- **Proposed Changes**:
  - [ ] No immediate changes — informational; consider simplifying `code-reviewer` dispatch to use `@agent-name` syntax when the pattern matures
- **Priority**: Low — informational; syntax improvement for future consideration

---

### Recommendations

Top 3 changes to make now:

1. **Verify Haiku alias resolution before April 19** — Check that `model: haiku` in `feedback.md`, `version.md`, and `code-reviewer.md` (Convention + History agents) resolves to Haiku 4.5, not Haiku 3. If Claude Code model aliases aren't guaranteed to skip retired models, update to `model: haiku-4-5` or equivalent.

2. **`shared/claude-code/hooks/pre-commit-secrets-scan.sh`** — Implement `permissionDecision: "defer"` for non-commit Bash calls. Currently the secrets scan runs on every Bash tool call; it should only gate `git commit` commands and defer everything else, reducing latency on all other operations.

3. **`shared/claude-code/knowledge-base/repos.md`** — Add the two new engineering blog posts as reference links: "Effective Harnesses for Long-Running Agents" and "How We Built Our Multi-Agent Research System" — both directly relevant to the REPAIR pipeline architecture.

---

## Latest Scan: 2026-03-30

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 15
- Actionable integrations: 8

### Findings

#### Claude Computer Use in Cowork & Claude Code
- **Source**: https://www.anthropic.com/news
- **Published**: March 23, 2026
- **Category**: Agent
- **What Changed**: Research preview giving Claude the ability to see and control a Mac desktop — opening apps, clicking buttons, filling forms, running browsers, editing files, and completing multi-step autonomous workflows. Available to Pro and Max subscribers.
- **Impact on ag3nts**: Relevant to `software-architect` and `security-engineer` agents which could leverage computer use for broader automated auditing. The `reality-checker` agent could use desktop control to validate production UIs.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add note that computer use is available on Pro/Max; consider whether any agents should declare computer use capability
- **Priority**: Medium — research preview; wait for GA before deep integration

---

#### Auto Mode for Claude Code
- **Source**: https://www.anthropic.com/news, https://www.anthropic.com/engineering/claude-code-auto-mode
- **Published**: March 24, 2026
- **Category**: Agent / Tooling
- **What Changed**: Classifier-based "middle path" between manual approval and full autonomy. Classifiers auto-approve safe tool calls and block risky ones (mass file deletions, data exfiltration, malicious code). Research preview for Team users; rolling out to Enterprise and API. Works only with Sonnet 4.6 and Opus 4.6.
- **Impact on ag3nts**: Directly affects the auto-invoke rules in `ag3nts.md` and how `code-reviewer` and `security-engineer` agents gate commits/pushes. Auto mode could reduce friction on safe commits while preserving the security gates already in place.
- **Proposed Changes**:
  - [ ] `shared/claude-code/CLAUDE.md` — add note to enable auto mode once available on API tier; document that auto mode complements (not replaces) the existing `code-reviewer` + `security-engineer` gates
  - [ ] `shared/ag3nts.md` — document auto mode as a Tooling capability with Sonnet 4.6 / Opus 4.6 requirement
- **Priority**: High — directly improves the commit/push workflow this system relies on

---

#### 1M Context Window Generally Available (Opus 4.6, Sonnet 4.6)
- **Source**: https://claude.com/blog/1m-context-ga
- **Published**: March 13, 2026
- **Category**: Model / API
- **What Changed**: 1M token context window is now GA at standard per-token pricing (no beta header, no premium tier). Up to 600 images/PDF pages per request. Claude Code on Max/Team/Enterprise defaults to 1M context automatically.
- **Impact on ag3nts**: `software-architect` (Opus) and `security-engineer` (Opus) can now ingest entire large codebases in a single pass. The `code-reviewer` (Sonnet) can review full repo diffs without chunking. Removes need for any context management workarounds.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — remove any caveats about context limits for Opus/Sonnet agents; update agent table notes if any reference chunking or context limits
  - [ ] `shared/claude-code/CLAUDE.md` — update the `/compact` guidance (currently triggered at 60% usage); 1M context makes this less urgent for most tasks
- **Priority**: High — unblocks large-codebase workflows for Opus-based agents

---

#### Interactive Charts, Diagrams & Visualizations in Chat
- **Source**: https://www.anthropic.com/news
- **Published**: March 12, 2026
- **Category**: Model / Tooling
- **What Changed**: Claude can generate interactive charts, diagrams, and visualizations inline using HTML/SVG/CSS/JS. Available to all users including free tier. Visuals update conversationally.
- **Impact on ag3nts**: The `ux-architect` agent could leverage inline visualizations for design token previews and layout system diagrams. The `software-architect` agent could produce interactive architecture diagrams.
- **Proposed Changes**:
  - [ ] Agent definitions for `ux-architect` and `software-architect` in `~/.claude/agents/` — add guidance to use inline visualizations for diagrams/wireframes where applicable
- **Priority**: Medium — enhances output quality but not a workflow blocker

---

#### Code with Claude 2026 Conference (SF/London/Tokyo)
- **Source**: https://www.anthropic.com/news
- **Published**: March 18, 2026
- **Category**: Tooling
- **What Changed**: Second annual developer conference expanding to SF (May 6), London (May 19), Tokyo (June 10).
- **Impact on ag3nts**: No direct integration; informational.
- **Proposed Changes**: None
- **Priority**: Low — informational only

---

#### Anthropic Economic Index: "Learning Curves" — March 2026 Report
- **Source**: https://www.anthropic.com/research
- **Published**: March 2026
- **Category**: Safety
- **What Changed**: Coding usage migrated from Claude.ai to Claude Code API. ~49% of jobs have had at least a quarter of tasks performed with Claude. Use case diversification accelerating.
- **Impact on ag3nts**: Signals that Claude Code API usage is the dominant pattern — validates the ag3nts setup. No direct code changes needed.
- **Proposed Changes**: None
- **Priority**: Low — informational/contextual

---

#### Web Search Tool & Tool Calling — Generally Available
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API / Tooling
- **What Changed**: Web search and tool calling moved out of beta — no beta header required. Web search supports dynamic filtering (code execution filters results before context window, reducing token cost). API code execution is free when used with web search or web fetch.
- **Impact on ag3nts**: The `accessibility-auditor` (uses WCAG refs) and `security-engineer` (uses CVEs) agents use web search. Removing the beta header simplifies their API calls. Cost reduction from dynamic filtering benefits high-frequency agents.
- **Proposed Changes**:
  - [ ] Agent definitions for `accessibility-auditor` and `security-engineer` — remove any `anthropic-beta: web-search-2025-03-05` or similar beta headers from tool configs
- **Priority**: High — breaking change once beta deprecated; clean up now

---

#### Structured Outputs — Generally Available
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API
- **What Changed**: Structured outputs GA for Sonnet 4.5, Opus 4.5, Haiku 4.5. `output_format` parameter moved to `output_config.format`. Improved schema compilation latency.
- **Impact on ag3nts**: The `feedback` agent (Haiku) and `version` agent (Haiku) could produce structured JSON output for audit logs and preference storage. `code-reviewer` (Sonnet) could emit structured review reports.
- **Proposed Changes**:
  - [ ] Agent definitions for `feedback`, `version`, `code-reviewer` — evaluate adopting `output_config.format` for structured JSON output
- **Priority**: Medium — enables richer programmatic output; not urgent

---

#### Claude Code Analytics API
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API / Tooling
- **What Changed**: New API endpoint for programmatic access to daily aggregated Claude Code usage metrics — productivity metrics, tool usage stats, cost data.
- **Impact on ag3nts**: Could be used by the `version` agent (agent inventory audit) to pull actual usage metrics and supplement drift detection with cost/frequency data.
- **Proposed Changes**:
  - [ ] `~/.claude/agents/version` agent definition — add Claude Code Analytics API as a data source for usage auditing
- **Priority**: Medium — enhances the `version` agent's audit capabilities

---

#### Model Capability Fields in Models API
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API
- **What Changed**: `GET /v1/models` now returns `max_input_tokens`, `max_tokens`, and a `capabilities` object for programmatic model discovery.
- **Impact on ag3nts**: The `version` agent performs consistency checks on agent configurations. It could now query the Models API to verify that model IDs declared in `ag3nts.md` are valid and have the expected capabilities.
- **Proposed Changes**:
  - [ ] `~/.claude/agents/version` agent definition — add Models API check to the consistency audit routine
- **Priority**: Low — nice-to-have validation enhancement

---

#### Extended Thinking `display` Field
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API / Model
- **What Changed**: New `thinking.display: "omitted"` option omits thinking content from responses while preserving signatures for multi-turn continuity. Enables faster streaming.
- **Impact on ag3nts**: `software-architect` (Opus) and `security-engineer` (Opus) use extended thinking for complex analysis. Setting `display: "omitted"` could reduce latency and token costs for intermediate thinking steps in multi-turn agent conversations.
- **Proposed Changes**:
  - [ ] Agent definitions for `software-architect` and `security-engineer` — consider adding `thinking.display: "omitted"` for multi-turn sessions where thinking output isn't needed by downstream agents
- **Priority**: Medium — latency/cost optimization for Opus agents

---

#### Context Editing (Beta)
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API / Agent
- **What Changed**: Beta feature that automatically clears older tool results and calls when approaching token limits, managing conversation context programmatically.
- **Impact on ag3nts**: Relevant given the 1M context window GA. For very long agent sessions (multi-stage REPAIR workflows), context editing could prevent token limit failures without manual `/compact` intervention.
- **Proposed Changes**:
  - [ ] `shared/claude-code/CLAUDE.md` — note context editing beta as alternative to manual `/compact`; update guidance accordingly
- **Priority**: Low — 1M context makes this less urgent, but useful for very long sessions

---

#### Tool Helpers Beta (Python & TypeScript SDKs)
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: March 2026
- **Category**: API / Tooling
- **What Changed**: New SDK helpers for type-safe tool creation, input validation, and automated tool handling in conversations.
- **Impact on ag3nts**: Any Python or TypeScript code in this repo that uses the Anthropic SDK for tool calling could be simplified. Particularly relevant if agent definitions evolve to include programmatic tool runners.
- **Proposed Changes**:
  - [ ] Any Python/TS files using `anthropic` SDK tool patterns — evaluate migration to tool helpers when next touching those files
- **Priority**: Low — convenience improvement; evaluate on next SDK touch

---

#### Data Residency Controls (`inference_geo`)
- **Source**: https://docs.anthropic.com/en/release-notes/overview
- **Published**: ~March 2026
- **Category**: API
- **What Changed**: New `inference_geo` parameter lets customers specify where model inference runs (US-only at 1.1x pricing).
- **Impact on ag3nts**: No current compliance requirements noted in `ag3nts.md`. Informational for now.
- **Proposed Changes**: None
- **Priority**: Low — no current compliance requirement

---

### Recommendations

Top 3 changes to make now:

1. **`shared/ag3nts.md`** — Update agent table and notes to reflect 1M context GA for Opus 4.6 and Sonnet 4.6 (remove any context-limit caveats). Add Auto Mode as a documented Tooling capability requiring Sonnet 4.6 / Opus 4.6.

2. **Agent definitions for `accessibility-auditor` and `security-engineer`** (`~/.claude/agents/`) — Remove beta headers for web search tool (`anthropic-beta: web-search-*`); web search is now GA and beta headers may cause deprecation warnings or errors.

3. **`shared/claude-code/CLAUDE.md`** — Add note to enable Claude Code Auto Mode once available on the API tier, documenting that it complements (not replaces) the existing `code-reviewer` + `security-engineer` gates. Update `/compact` guidance given 1M context GA.
