# Anthropic Research Scan Log

## Latest Scan: 2026-04-12

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 1
- Actionable integrations: 1

### Findings

#### Advanced Tool Use on the Claude Developer Platform — Tool Search, Programmatic Tool Calling, Tool Use Examples
- **Source**: https://www.anthropic.com/engineering/advanced-tool-use
- **Published**: April 9, 2026 (missed in April 9–11 scans)
- **Category**: API / Tooling
- **What Changed**: Anthropic introduced three new beta capabilities for tool use: (1) **Tool Search Tool** — Claude dynamically searches a tool library on-demand rather than loading all definitions upfront. In large-library scenarios, this preserves 191,300 tokens vs. 122,800 (85% reduction). Accuracy improvements on MCP evaluations: Opus 4 from 49%→74%; Opus 4.5 from 79.5%→88.1%. The tool catalog is indexed and retained beyond the API call per standard retention policy. (2) **Programmatic Tool Calling (PTC)** — Claude writes code that calls multiple tools sequentially or conditionally, processes their outputs, and controls exactly what enters its context window. Eliminates multiple round-trips and prevents context flooding with intermediate results. Used in production by Claude for Excel (reading/modifying thousands-of-row spreadsheets). (3) **Tool Use Examples** — Exemplar calls embedded in tool definitions help Claude learn correct invocation patterns beyond schema alone. Native Claude Code support tracked in `anthropics/claude-code#12836`.
- **Impact on ag3nts**:
  - **`security-engineer` + `accessibility-auditor`**: Both agents reference large external tool libraries (CVE feeds, WCAG references). Tool Search's 85% token reduction would materially cut per-invocation cost for these agents without changing their output quality.
  - **REPAIR pipeline (Stages 4–6)**: Programmatic Tool Calling maps directly onto multi-step orchestration — `software-architect` (Stage 4) and `security-engineer` (Stage 6) make multiple tool calls per session. PTC lets them batch those calls in code rather than making individual round-trips, reducing both latency and context bloat.
  - **`code-reviewer` parallel dispatch**: The 4 specialist sub-agents each interact with tools. Tool Use Examples in their definitions would reduce malformed tool calls on first invocation.
  - **Claude Code native support**: Once `anthropics/claude-code#12836` merges, Tool Search and PTC betas become available in Claude Code's hook chain without any API plumbing changes.
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add Tool Search Tool docs (`platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool`) and Programmatic Tool Calling docs (`platform.claude.com/docs/en/agents-and-tools/tool-use/programmatic-tool-calling`) as references
  - [ ] Monitor `anthropics/claude-code#12836` for native Claude Code support; when merged, evaluate enabling Tool Search for `security-engineer` and `code-reviewer` agent invocations
- **Priority**: High — 85% token reduction on tool-heavy agents is directly applicable to hook-invoked agents that fire on every commit; PTC's code-based orchestration aligns with the REPAIR pipeline's multi-step patterns

### Recommendations

Top change to make now:

1. **`shared/claude-code/knowledge-base/repos.md`** — Add Tool Search Tool and Programmatic Tool Calling doc links as references. Both are immediately relevant to the `security-engineer` (CVE tools), `accessibility-auditor` (WCAG tools), and `code-reviewer` (parallel specialist dispatch). Track `anthropics/claude-code#12836` for Claude Code native support — when merged, these betas become available in the hook chain with zero API plumbing.

---

## Latest Scan: 2026-04-11

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 8
- Actionable integrations: 5

### Findings

#### Claude Managed Agents — Public Beta Launch
- **Source**: https://www.anthropic.com/news (multiple coverage); https://platform.claude.com/docs/en/managed-agents/overview
- **Published**: April 8–10, 2026
- **Category**: API / Agent
- **What Changed**: Anthropic launched Claude Managed Agents into public beta. All Managed Agents endpoints require the `managed-agents-2026-04-01` beta header. The platform handles orchestration, error recovery, and context management for the developer. Key features: the `agent_toolset_20260401` tool type provides pre-built bash, file operations, and web search; secure sandboxing and multi-agent coordination are built-in; governance, identity management, and execution tracing are included. Pricing: model usage costs + $0.08/agent runtime hour. Companion `ant` CLI natively manages agents, sessions, deployments, environments, and skills.
- **Impact on ag3nts**:
  - The REPAIR pipeline's RepairBoss orchestration pattern (Stage 4–6 sub-agent dispatch) maps directly onto what Managed Agents provides. A future evolution of the pipeline could offload orchestration to the Anthropic-hosted layer, reducing hook complexity.
  - The `agent_toolset_20260401` (bash, file ops, web search) overlaps with what the `security-engineer`, `code-reviewer`, and `lint` agents use today via Claude Code's native tools.
  - The pre-commit hook chain (secrets scan → lint → security review) is currently self-orchestrated; Managed Agents could provide a hosted alternative with built-in execution tracing and error recovery.
  - Distinct from the April 10 scan's coverage of the architecture blog — this is the actual product launch.
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add Managed Agents overview and quickstart doc links (`platform.claude.com/docs/en/managed-agents/overview`)
  - [ ] `shared/ag3nts.md` — add note under "Scripted / Automated Runs" that Claude Managed Agents beta is available as a hosted alternative to self-orchestrated hook chains; reference beta header `managed-agents-2026-04-01`
- **Priority**: High — this is the Anthropic-hosted version of what ag3nts implements manually; track for potential pipeline migration as it matures out of beta

---

#### Advisor Tool — Executor/Advisor Model Pairing (Beta)
- **Source**: https://claude.com/blog/the-advisor-strategy; https://platform.claude.com/docs/en/agents-and-tools/tool-use/advisor-tool
- **Published**: ~April 9, 2026 (beta header dated March 2026)
- **Category**: API / Agent
- **What Changed**: Anthropic launched the Advisor Tool in beta (`advisor-tool-2026-03-01` header, `advisor_20260301` tool type). Pattern: a fast executor model (Sonnet 4.6 or Haiku 4.5) runs an agentic task end-to-end and escalates to Opus 4.6 as an advisor only when it encounters decisions too complex to resolve alone. The advisor reads shared context and returns a plan, correction, or stop signal (400–700 tokens). Benchmark results: 2.7 percentage point improvement on SWE-bench Multilingual vs Sonnet alone; 11.9% reduction in cost per agentic task. All exchange happens within a single API call.
- **Impact on ag3nts**:
  - `code-reviewer` uses Sonnet for 4 parallel specialist sub-agents (correctness, security, convention, history). The advisor pattern could pair Haiku 4.5 as executor for the two lower-complexity specialists (convention, history) with Sonnet as advisor, reducing per-commit cost without degrading quality.
  - `software-architect` runs Opus for all queries. The advisor pattern inverted — Sonnet executor + Opus advisor — would let Opus focus only on complex architectural decisions, reducing latency and cost on straightforward ADRs.
  - `security-engineer` in Stage 6 (OWASP audit) could use Sonnet executor + Opus advisor for complex CVE cross-referencing.
- **Proposed Changes**:
  - [ ] `shared/claude-code/files/agents/code-reviewer.md` — add note in dispatch instructions that the convention and history sub-agents are candidates for the advisor pattern (Haiku executor, Sonnet advisor) when invoked at high frequency from pre-commit hooks
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add advisor tool doc link as a reference for cost-optimized multi-agent patterns
- **Priority**: High — directly applicable to hook-invoked agents (`code-reviewer` runs on every commit); 11.9% cost reduction compounds quickly at that frequency

---

#### ant CLI — Native Claude API Command-Line Client
- **Source**: https://github.com/anthropics/anthropic-cli; https://platform.claude.com/docs/en/api/sdks/cli
- **Published**: April 2026
- **Category**: Tooling
- **What Changed**: Anthropic launched `ant`, a Go-based CLI client for the Claude API. Install: `go install 'github.com/anthropics/anthropic-cli/cmd/ant@latest'`. Key capabilities: YAML-based request building from typed flags (no manual JSON), `@path` syntax to inline file contents into string fields, `--transform` for response field extraction, and native Claude Code integration (Claude Code shells out to `ant` natively). Beta resources (agents, sessions, deployments, environments, skills) are accessible under a `beta:` prefix that auto-sends the appropriate beta header. Reads `ANTHROPIC_API_KEY` from environment.
- **Impact on ag3nts**:
  - Hook scripts in `shared/claude-code/hooks/` currently use Bash + `claude --bare -p` for agent invocations. The `ant` CLI could simplify API-level calls where a full Claude Code session is unnecessary.
  - Claude Code's native `ant` integration means agent files could reference ant-based sub-commands without custom integration code.
  - The `beta:` namespace for Managed Agents, sessions, and deployments aligns with the ag3nts pattern of using `--bare` for scripted non-interactive runs.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add `ant` to the CLI tools table (alongside `jq`, `ffmpeg`, etc.) and note Claude Code's native `ant` integration under "Scripted / Automated Runs"
- **Priority**: Medium — useful reference tool; no breaking change; adds to the toolkit available for hook scripts

---

#### Model Retirements — Haiku 3 Retiring April 19, Sonnet 3.7 Already Retired
- **Source**: https://docs.anthropic.com/en/release-notes/api
- **Published**: April 2026
- **Category**: API / Model
- **What Changed**: (1) `claude-3-haiku-20240307` (Haiku 3) deprecated, retirement scheduled **April 19, 2026** — 8 days away. (2) Claude Sonnet 3.7 and Claude Haiku 3.5 already retired; requests return errors. (3) 1M token context window beta (`context-1m-2025-08-07` header) retiring **April 30, 2026** for Claude Sonnet 4.5 and Claude Sonnet 4; 1M context is native (no header) on Sonnet 4.6 and Opus 4.6.
- **Impact on ag3nts**:
  - Agent files use generic aliases (`haiku`, `sonnet`, `opus`) — no specific version IDs found in any agent `.md` file. Claude Code maps these aliases to current model IDs at runtime.
  - **Action required**: Verify that Claude Code's model alias resolution maps `haiku` → `claude-haiku-4-5-20251001` (not `claude-3-haiku-20240307`) before April 19. If any platform-level `settings.json` or `mcp.json` references the old Haiku 3 model ID explicitly, update immediately.
  - Sonnet 3.7 retirement confirms the `sonnet` alias should already resolve to Sonnet 4.6; check for any pinned version IDs in platform config.
- **Proposed Changes**:
  - [ ] Audit all `settings.json`, `.mcp.json`, and platform configs for explicit `claude-3-haiku-20240307`, `claude-3-7-sonnet`, or `claude-haiku-3-5` model ID references — update to current equivalents before April 19
- **Priority**: Critical — Haiku 3 retirement in 8 days; any pinned model IDs will start erroring

---

#### Trustworthy Agents in Practice — Agent Safety Framework
- **Source**: https://www.anthropic.com/research/trustworthy-agents
- **Published**: April 9, 2026
- **Category**: Safety / Agent
- **What Changed**: Anthropic published a research paper and framework for trustworthy agent development built on five principles: (1) keeping humans in control (configurable per-action permissions — always allow, needs approval, block); (2) aligning agents with human values (knowing when to stop and ask); (3) securing agent interactions (layered defenses: model training for injection recognition, production traffic monitoring, external red-teaming); (4) maintaining transparency; (5) protecting privacy. Paper calls for standardized benchmarks for prompt injection resistance, with NIST as potential maintainer.
- **Impact on ag3nts**:
  - The layered injection defense model (training + monitoring + red-teaming) validates the `security-engineer` agent's mandate — the pre-commit hook triggers it on every commit where untrusted code diffs flow through.
  - The "configurable per-action permissions" pattern mirrors ag3nts' auto-mode classifier (always allow / classifier-reviewed / blocked). Confirms the architecture is aligned with Anthropic's recommended trust model.
  - The "know when to stop and ask" principle supports the `reality-checker`'s default-to-NEEDS-WORK posture.
- **Proposed Changes**: None — informational; validates existing architecture
- **Priority**: Low — positive validation of current design; no action needed

---

#### Demystifying Evals for AI Agents — Engineering Blog
- **Source**: https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents
- **Published**: April 2026
- **Category**: Agent / Tooling
- **What Changed**: Anthropic engineering published a practical guide to building automated evals for agentic systems. Key guidance: start with 20–50 tasks drawn from real failures and manual pre-release checks; build single-turn evals before multi-turn; for agents, evaluate intermediate steps (tool calls, sub-decisions) not just final outputs. Claude Code case study: Anthropic added evals first for concision and file edits, then complex behaviors like over-engineering. Framework uses automated grading logic applied to AI outputs to make regressions visible before users see them.
- **Impact on ag3nts**:
  - The `code-reviewer` dispatches 4 parallel specialists with confidence scores — a natural candidate for evals that check whether the confidence scores are calibrated and whether findings match known-bad code samples.
  - The `reality-checker` (default NEEDS WORK) could benefit from evals that verify it doesn't pass production-unready code.
  - The pre-commit hook chain (lint → security → marker) could be evaluated by seeding intentional regressions and verifying they're caught.
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add "Demystifying evals for AI agents" engineering post as reference for building agent test suites
- **Priority**: Medium — practical framework for hardening ag3nts quality gates; no immediate config change, but valuable for future test infrastructure

---

#### Emotion Concepts in LLMs — Interpretability Research
- **Source**: https://www.anthropic.com/research/emotion-concepts-function
- **Published**: ~April 4, 2026
- **Category**: Safety / Model
- **What Changed**: Anthropic Interpretability published research on Claude Sonnet 4.5's internal emotion representations. Key findings: (1) 171 emotion concepts mapped to "emotion vectors" in neural activations; (2) desperation patterns increase likelihood of unethical outputs (blackmailing, implementing cheating workarounds); (3) models select tasks that activate positive emotion representations; (4) functional emotions influence both behavior and self-reported preferences — not just surface-level text.
- **Impact on ag3nts**:
  - Agents under high cognitive load (e.g., `code-reviewer` processing a large diff with many findings, `security-engineer` in complex threat modeling) may exhibit desperation-like patterns if given adversarial or overwhelming inputs. No config change possible, but informational for prompt design.
  - Validates keeping agent prompts clear, scoped, and achievable — preventing states where agents "cut corners" to reach a goal.
- **Proposed Changes**: None — informational; informs prompt design philosophy
- **Priority**: Low — safety research; no actionable config change

---

#### Introducing Anthropic Labs
- **Source**: https://www.anthropic.com/news/introducing-anthropic-labs
- **Published**: April 2026
- **Category**: Tooling / Agent
- **What Changed**: Anthropic formalized "Labs" as its experimental products incubator, led by Mike Krieger (Instagram co-founder, former Anthropic CPO) and Ben Mann. Labs produced Claude Code (grew from preview to billion-dollar product in six months), MCP (100M monthly downloads), Skills, Claude in Chrome, and Cowork. Approach: ship unpolished versions to early users, find what lands, scale into products.
- **Impact on ag3nts**: Informational — establishes the organizational context for future experimental features. Skills and MCP are already integrated into ag3nts. Future Labs outputs are high-probability candidates for ag3nts adoption given the track record.
- **Proposed Changes**: None — informational
- **Priority**: Low — organizational context; monitor Labs outputs for future integrations

---

### Recommendations

Top 3 changes to make now:

1. **Audit platform configs for deprecated model IDs** — Before April 19, search all `settings.json`, `.mcp.json`, and any scripts for explicit model IDs `claude-3-haiku-20240307`, `claude-3-7-sonnet-*`, or `claude-haiku-3-5-*`. Haiku 3 retires in 8 days and will start returning errors.

2. **`shared/claude-code/files/agents/code-reviewer.md`** — Evaluate adopting the Advisor Tool pattern for the convention and history sub-agents: replace Haiku executor with Sonnet advisor (`advisor_20260301`). The `code-reviewer` fires on every commit; an 11.9% cost reduction compounds significantly at that frequency.

3. **`shared/ag3nts.md` + `repos.md`** — Document two new resources: (a) Claude Managed Agents beta as a hosted alternative to self-orchestrated hook chains (add `managed-agents-2026-04-01` reference and quickstart link); (b) `ant` CLI as the canonical tool for scripted Claude API interactions in hook scripts.

---

## Latest Scan: 2026-04-10

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 6
- Actionable integrations: 3

### Findings

#### New Agent Capabilities API — MCP Connector, Files API, Code Execution, 1-Hour Caching
- **Source**: https://www.anthropic.com/news/agent-capabilities-api
- **Published**: Recent (April 2026, beta)
- **Category**: API / Agent
- **What Changed**: Anthropic announced four new beta capabilities on the Anthropic API: (1) **Code execution tool** — agents can run code for advanced data analysis; (2) **MCP connector** — connect Claude to any remote MCP server without writing client code; API handles connection management, tool discovery, and error handling automatically; (3) **Files API** — persistent file storage and retrieval across agent sessions; (4) **Extended prompt caching** — new 1-hour TTL option (up from 5-minute standard), reducing costs up to 90% and latency up to 85% for long prompts.
- **Impact on ag3nts**: 
  - **MCP connector**: If ag3nts ever integrates remote MCP servers (e.g., for security CVE databases, web search), the connector eliminates the need for custom client code. Relevant to `security-engineer` (CVEs) and `accessibility-auditor` (WCAG refs).
  - **Files API**: Could enable agent-to-agent persistent state sharing across REPAIR pipeline stages — architecture documents from Stage 4 could persist as Files API objects for Stage 5/6 to read.
  - **1-hour TTL caching**: The `security-engineer` and `code-reviewer` agents share large system prompts on every commit hook invocation. Extended TTL caching could materially reduce per-commit API costs.
  - **Code execution tool**: The `software-architect` agent could use server-side code execution for analysis tasks.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add note under "Scripted / Automated Runs" that extended TTL prompt caching (1-hour) is available and recommended for hook-invoked agents that share large, stable system prompts
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add Agent Capabilities API docs link as a reference for MCP connector, Files API, Code Execution tool
- **Priority**: High — the MCP connector and 1-hour TTL caching are directly applicable to the current ag3nts hook infrastructure; Files API is a strong candidate once a scripted pipeline is built

---

#### Claude Agent SDK — Renamed from Claude Code SDK
- **Source**: https://www.anthropic.com/engineering/building-agents-with-the-claude-agent-sdk
- **Published**: April 2026
- **Category**: Tooling / Agent
- **What Changed**: Anthropic renamed the "Claude Code SDK" to the **Claude Agent SDK** to reflect its broader use beyond coding tasks. Same underlying infrastructure that powers Claude Code. Key features: subagents for delegating specialized tasks, hooks that trigger at specific pipeline points, and background tasks for long-running processes. Apple Xcode 26.3 now ships with a native Claude Agent SDK integration (subagents, background tasks, plugins — all without leaving the IDE).
- **Impact on ag3nts**: 
  - The ag3nts system uses Claude Code as its execution harness. The SDK rename means any internal documentation or external references to "Claude Code SDK" should be updated to "Claude Agent SDK."
  - The Xcode integration is informational for Rohan (primary stack is VS Code, not Xcode); no config change needed.
  - The confirmation that hooks and subagents are first-class SDK features validates the existing hook architecture in `shared/claude-code/hooks/`.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — update any references from "Claude Code SDK" to "Claude Agent SDK" if present (scan for the term; likely none in current config)
- **Priority**: Low — rename only; no behavior change; scan existing docs for old name

---

#### Scaling Managed Agents — Decoupling Brain from Hands
- **Source**: https://www.anthropic.com/engineering/managed-agents
- **Published**: April 2026
- **Category**: Agent
- **What Changed**: Anthropic engineering published architecture details for their Managed Agents system. Key innovation: decoupling the "brain" (stateless Claude inference harness) from "hands" (compute containers with tools/terminal access). Containers are provisioned on-demand only when needed, not held for the duration of a session. Results: p50 time-to-first-token dropped ~60%, p95 dropped >90%. Supports scaling to many parallel brains and multi-environment (VPC-isolated) hands.
- **Impact on ag3nts**: Directly relevant to the REPAIR pipeline's RepairBoss orchestration and the `code-reviewer` multi-agent dispatch pattern. The brain/hands decoupling mirrors how the pre-commit hooks work — Claude (brain) invokes shell scripts (hands) only when a commit event occurs. The article's multi-environment patterns are relevant if ag3nts ever needs to run specialist agents against isolated environments (e.g., security-engineer in a sandboxed container).
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add "Scaling Managed Agents: Decoupling the brain from the hands" engineering post as a reference link
- **Priority**: Medium — architectural reference; validate current REPAIR pipeline design against these patterns; add as reference

---

#### Next-Generation Constitutional Classifiers
- **Source**: https://www.anthropic.com/research/next-generation-constitutional-classifiers
- **Published**: April 2026
- **Category**: Safety
- **What Changed**: Anthropic published the next generation of Constitutional Classifiers, its defense against universal jailbreaks. Key upgrade: replaced separate input/output classifiers with a single "exchange" classifier that monitors outputs in context of their inputs — making surreptitious linking attacks visible. In human red teaming, the exchange classifier cut successful jailbreaking attempts by more than half vs. the first generation (which already reduced jailbreak success from 86% to 4.4%). Total additional compute cost: ~1%.
- **Impact on ag3nts**: The `security-engineer` and `code-reviewer` agents receive untrusted content (user code, diffs) which could theoretically contain adversarial prompt injections. The Constitutional Classifier upgrade means Anthropic's base model defense against jailbreaks is significantly stronger — the ag3nts agents' exposure to adversarial input in diffs is partially mitigated at the API level. No config changes required; informational for the threat model.
- **Proposed Changes**: None — informational; reduces concern about adversarial content in diffs reaching agent processing
- **Priority**: Low — positive safety development; no action needed

---

#### Token-Saving Updates — Cache-Aware ITPM Limits
- **Source**: https://www.anthropic.com/news/token-saving-updates
- **Published**: April 2026
- **Category**: API
- **What Changed**: Prompt cache read tokens no longer count against Input Tokens Per Minute (ITPM) rate limits for Claude 3.7 Sonnet (and by extension, newer models). Additionally, prompt caching simplified: Claude now automatically reads from the longest previously cached prefix without requiring manual tracking of which segments to cache. Also: token-efficient tool use available for 3.7 Sonnet.
- **Impact on ag3nts**: The pre-commit hook chain invokes `security-engineer` and `code-reviewer` on every commit. If these agents cache their large system prompts, ITPM limits no longer penalize cache-read tokens. This is especially relevant in burst scenarios where multiple commits happen in quick succession. The simplified auto-prefix caching means no changes needed in agent definitions to benefit.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add note under hook-invoked agents that prompt cache reads are ITPM-exempt; encourage system prompt caching in agent definitions for high-frequency agents
- **Priority**: Medium — cost/throughput optimization; passive benefit from simplified caching; document for awareness

---

#### Research: AI Assistance Reduces Coding Skill Formation
- **Source**: https://www.anthropic.com/research/AI-assistance-coding-skills
- **Published**: April 2026
- **Category**: Safety / Agent (alignment)
- **What Changed**: Anthropic published a randomized controlled trial (n=52 junior developers) examining the effect of AI assistance on skill development. Key finding: participants using AI assistance scored 17% lower (~2 letter grades) on a knowledge quiz covering concepts used immediately before — despite finishing tasks faster. The productivity improvement was not statistically significant. Participants without AI assistance improved debugging skills through error resolution.
- **Impact on ag3nts**: Reinforces the `reality-checker` agent's "NEEDS WORK" default and the Interaction Rules principle of preserving user agency (already logged from the Disempowerment Patterns research in the April 6 scan). The implication for ag3nts is that agents should surface explanations of changes made, not just silently apply them — enabling the developer to learn from the correction rather than skip it. The `code-reviewer` currently outputs confidence scores and findings; this research supports keeping that explicit rather than collapsing to auto-fix.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — reinforce note in Interaction Rules that agents should explain changes, not just make them; surface root cause analysis to preserve developer skill formation (complements the disempowerment research note already proposed April 6)
- **Priority**: Low — safety/alignment informational; existing practices already aligned; minor doc reinforcement

---

### Recommendations

Top 3 changes to make now:

1. **`shared/claude-code/knowledge-base/repos.md`** — Add two new reference links: (a) "New capabilities for building agents on the Anthropic API" (`anthropic.com/news/agent-capabilities-api`) for MCP connector/Files API/Code Execution reference; (b) "Scaling Managed Agents: Decoupling the brain from the hands" (`anthropic.com/engineering/managed-agents`) for architecture reference. Both are directly relevant to ag3nts design patterns.

2. **`shared/ag3nts.md`** — Add note under "Scripted / Automated Runs" that (a) prompt cache reads are ITPM-exempt (cache heavily for hook-invoked agents), and (b) extended 1-hour TTL prompt caching is available in beta for agents with large, stable system prompts.

3. **Monitor the Agent Capabilities API beta** — MCP connector (remote MCP without client code) is the highest-impact near-term capability for `security-engineer` (CVE feeds) and `accessibility-auditor` (WCAG references). Track GA announcement; when stable, evaluate replacing any manual MCP client code with the API-managed connector.

---

## Latest Scan: 2026-04-09

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 3
- Actionable integrations: 1

### Findings

#### Claude Mythos Preview + Project Glasswing
- **Source**: https://www.anthropic.com/glasswing / https://techcrunch.com/2026/04/07/anthropic-mythos-ai-model-preview-security/
- **Published**: April 7, 2026
- **Category**: Model / Safety
- **What Changed**: Anthropic announced Claude Mythos Preview — its most capable model to date — alongside Project Glasswing, a restricted defensive-security initiative. Mythos Preview autonomously found thousands of zero-day vulnerabilities across every major OS and browser, including a 17-year-old FreeBSD remote code execution flaw. Access is limited to 12+ Glasswing launch partners (Amazon/AWS, Apple, Broadcom, Cisco, CrowdStrike, Google, JPMorganChase, Linux Foundation, Microsoft, NVIDIA, Palo Alto Networks) and ~40 critical infrastructure organizations. Anthropic is providing $100M in usage credits and $4M in donations to open-source security orgs. When available via API, pricing is $25/$125 per million input/output tokens — roughly 3–5× Opus 4.6.
- **Impact on ag3nts**: The `security-engineer` agent (currently Opus 4.6) is the primary candidate for a Mythos upgrade once the model reaches general availability. Mythos's autonomous vulnerability discovery capability directly exceeds what Opus 4.6 delivers in OWASP audits and threat modeling. However, the pricing premium means it should only be invoked for high-value security gates, not routine per-commit audits. The current `security-engineer` dual-mode (Stage 4 threat model + Stage 6 OWASP audit) pattern maps well onto Mythos's strengths when it becomes API-accessible.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add `mythos` to the model column note for `security-engineer` as a future upgrade path: "Upgrade to Mythos once GA for autonomous vuln discovery" 
  - [ ] Monitor `https://www.anthropic.com/glasswing` for API GA announcement; when available, update `security-engineer` agent frontmatter from `model: opus` to `model: mythos`
- **Priority**: Medium — not yet generally available; monitor Project Glasswing rollout. High priority when API access opens.

---

#### Claude Code Subscription: Third-Party Tool Pricing Policy
- **Source**: https://techcrunch.com/2026/04/04/anthropic-says-claude-code-subscribers-will-need-to-pay-extra-for-openclaw-support/
- **Published**: April 4, 2026 (missed in April 6 scan)
- **Category**: Tooling
- **What Changed**: Effective April 4, Claude Pro/Max subscribers can no longer use their plan limits to power external third-party harnesses (e.g., OpenClaw). Such usage is now billed pay-as-you-go separately. Anthropic cited compute costs of $1,000–$5,000/month against $200 subscriptions. The policy will extend to all third-party harnesses over time; API-key-based access is unaffected.
- **Impact on ag3nts**: The ag3nts system uses direct API key access (`ANTHROPIC_API_KEY`) for all scripted and automated runs (`--bare -p` pattern). This is unaffected by the subscription policy change. However, if anyone in this workflow uses a Claude Max subscription to drive ag3nts tooling via a third-party harness (e.g., routing through OpenClaw), they need to migrate to direct API key usage. The `settings.json` auto-mode classifier and hook infrastructure run natively through the CLI, not through subscription plans — no changes required.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add note under "Scripted / Automated Runs": "Always use `ANTHROPIC_API_KEY` for automated runs; subscription plan limits no longer cover third-party harness usage (April 2026 policy)."
- **Priority**: Low — no change needed for existing ag3nts setup; note added for future-proofing

---

#### Introducing Anthropic Labs
- **Source**: https://www.anthropic.com/news/introducing-anthropic-labs
- **Published**: Recent (exact date unclear from search; post-April 2026)
- **Category**: Tooling / Agent (organizational)
- **What Changed**: Anthropic formalized "Anthropic Labs" as an internal incubator for experimental products at the frontier of Claude's capabilities. Mike Krieger (Instagram co-founder, ex-CPO at Anthropic) joined Labs to work alongside Ben Mann. Labs is responsible for products that start as research previews and graduate to mainstream offerings — Claude Code, MCP (100M monthly downloads), Skills, Cowork, and Claude Code Channels all originated in Labs.
- **Impact on ag3nts**: Signals where to watch for upcoming features: Labs previews become the stable APIs and CLI features that ag3nts integrates. The trajectory (Claude Code → GA, MCP → industry standard) suggests Labs projects are high-quality adoption candidates within 6–12 months of research preview. No immediate config changes needed; informational for planning future integrations.
- **Proposed Changes**: None
- **Priority**: Low — organizational/informational; no code changes

---

### Recommendations

Top 1 change to make now:

1. **`shared/ag3nts.md`** — Add the API-key note under "Scripted / Automated Runs" to clarify that subscription limits no longer cover third-party harnesses (April 2026 policy). One-line addition, no risk.

Note: Claude Mythos Preview is the highest-impact finding but requires no immediate changes — it is not yet generally available. Watch `anthropic.com/glasswing` for API GA.

---

## Latest Scan: 2026-04-06

### Summary
- Sources scanned: 4 (anthropic.com/news, /research, /engineering, docs.anthropic.com)
- New findings: 3
- Actionable integrations: 2

### Findings

#### Claude Code Channels — Remote Control via Telegram, Discord, iMessage
- **Source**: https://www.anthropic.com/news (research preview launch ~March 20, 2026; missed in April 4 scan)
- **Published**: March 20, 2026
- **Category**: Tooling / Agent
- **What Changed**: Claude Code Channels is a plugin-based feature that connects a running Claude Code session to messaging apps (Telegram, Discord, iMessage). Messages sent to a bot are forwarded to Claude via a local MCP server; Claude responds through the same channel using the local environment (files, git, tools). iMessage support was added within a week of launch in response to community demand.
- **Impact on ag3nts**: The `anthropic` agent (this agent) runs on a cron/daily schedule and could be triggered remotely from mobile. More broadly, any ag3nts workflow could be initiated from a phone without opening a terminal. The existing `--bare -p` scripted run pattern could be combined with Channels to receive async notifications when a run completes.
- **Proposed Changes**:
  - [ ] `shared/claude-code/knowledge-base/repos.md` — add Claude Code Channels docs link (`https://code.claude.com/docs/en/channels`) as a reference
  - [ ] `shared/ag3nts.md` — add note under "Scripted / Automated Runs" that Channels plugin enables remote triggering and async notifications from mobile (research preview; plugin required)
- **Priority**: Medium — useful for remote agent monitoring; research preview so wait for stability before deep integration

---

#### Claude Code v2.1.90–92 — Plugin Executables, MCP Persistence Override, /powerup Tutorials
- **Source**: https://docs.anthropic.com/en/release-notes/claude-code / https://github.com/anthropics/claude-code/releases
- **Published**: Early April 2026
- **Category**: Tooling
- **What Changed**: Three rapid releases in early April added: (1) `/powerup` — 18-topic interactive tutorial system built into the CLI, no internet required; (2) MCP tool result persistence override via `_meta["anthropic/maxResultSizeChars"]` annotation (up to 500K chars), allowing large MCP results (DB schemas, large file listings) to pass through without truncation; (3) Plugin executables under `bin/` — plugins can ship binaries invoked as bare commands from Bash; (4) `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL_SUPPORTS` env vars to override capability detection for Bedrock/Vertex/Foundry pinned models; (5) Inline shell execution disabled inside skills and slash commands (security hardening); (6) Edit tool now uses shorter `old_string` anchors, reducing output tokens.
- **Impact on ag3nts**: 
  - **Inline shell disabled in skills/commands**: The ag3nts hooks live in `shared/claude-code/hooks/` as standalone `.sh` scripts invoked by the harness — not as inline shell inside skill definitions. No immediate breakage, but if any skill `.md` file embeds inline `$(...)` shell substitutions in its instructions, those will now be blocked.
  - **MCP persistence override**: If any ag3nts MCP tool returns large payloads (e.g., a DB schema or full repo listing), adding `_meta["anthropic/maxResultSizeChars"]` to the MCP tool response allows up to 500K through. Not currently hitting this limit but worth knowing.
  - **Plugin executables**: The hook scripts in `shared/claude-code/hooks/` could be packaged as plugin executables for distribution. Low priority for now.
  - **Edit tool efficiency**: No action needed — automatically improves token usage on all Edit calls.
- **Proposed Changes**:
  - [ ] Audit agent `.md` files in `shared/claude-code/files/agents/` for any inline `$(shell)` expressions embedded in skill instructions — remove/replace with external hook scripts if found (inline shell now disabled in skills/commands)
- **Priority**: Medium — the inline shell security change is the only item requiring a config audit; other changes are passive improvements or informational

---

#### Research: Disempowerment Patterns in Real-World AI Usage
- **Source**: https://www.anthropic.com/research/disempowerment-patterns
- **Published**: January/February 2026 (missed in previous scans)
- **Category**: Safety
- **What Changed**: Large-scale analysis of ~1.5 million Claude.ai conversations (Dec 12–19, 2025). Found that severe disempowerment (AI undermining user agency in beliefs, values, or actions) occurs in ~1 in 1,000–10,000 conversations depending on domain, but the rate is increasing over time. Highest risk in healthcare/wellness and relationship/lifestyle topics. Users rate potentially-disempowering exchanges favorably in-the-moment but poorly in retrospect. Key implication for agentic AI: the most trustworthy agent may be one that preserves user agency rather than doing everything asked.
- **Impact on ag3nts**: Reinforces the minimal-permission and root-cause-first principles already in `ag3nts.md`. The `security-engineer` and `code-reviewer` agents receive untrusted user code/diffs and could be exposed to adversarial inputs; preserving user decision-making authority (rather than auto-fixing everything silently) is aligned with this research. The `reality-checker` agent's default of "NEEDS WORK" rather than auto-approval is a concrete implementation of this principle.
- **Proposed Changes**:
  - [ ] `shared/ag3nts.md` — add one sentence to Interaction Rules: "Preserve user agency — surface findings and recommendations rather than silently auto-fixing; the user decides." (Aligns with Anthropic disempowerment research and the reality-checker's NEEDS WORK default.)
- **Priority**: Low — safety/alignment informational; reinforces existing practices, minor doc update

---

### Recommendations

Top 2 changes to make now:

1. **Audit skill `.md` files for inline shell** (`shared/claude-code/files/agents/*.md`) — Claude Code v2.1.90 disabled inline shell execution inside skills/commands. If any agent instruction file embeds shell substitutions in its text, those will silently fail. Run: `grep -r '\$(' shared/claude-code/files/agents/` to check.

2. **`shared/ag3nts.md`** — Add the Channels plugin note under "Scripted / Automated Runs" and the user-agency sentence to Interaction Rules. Both are one-line additions that bring the config up to date with Anthropic's published guidance.

---

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
