# Anthropic Research Scan Log

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
