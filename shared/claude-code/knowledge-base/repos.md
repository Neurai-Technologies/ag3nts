| Link | Description |
|------|-------------|
| https://openrouter.ai | Unified API gateway for multiple LLM providers |
| https://github.com/simonstaton/AgentManager | Agent management framework |
| https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents | Anthropic: planner/generator/evaluator harness patterns for multi-hour autonomous agent sessions |
| https://www.anthropic.com/engineering/multi-agent-research-system | Anthropic: GAN-style Generator/Evaluator architecture for long-running software engineering agents |
| https://resources.anthropic.com/2026-agentic-coding-trends-report | Anthropic: 2026 Agentic Coding Trends Report — 8 trends including multi-agent coordination, security-from-inception, and extended autonomous sessions |
| https://www.anthropic.com/news/skills | Anthropic: Agent Skills — open standard for portable specialized agents; organized folders of instructions/scripts/resources discoverable at runtime |
| https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills | Anthropic: Engineering post on Agent Skills design and implementation patterns |
| https://www.anthropic.com/news/agent-capabilities-api | Anthropic: New API agent capabilities — code execution tool, API-managed MCP connector, Files API, 1-hour prompt caching TTL |
| https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents | Anthropic: Effective context engineering — curating high-signal token sets for long-running agents; validates --bare and modular agent design |
| https://www.anthropic.com/news/token-saving-updates | Anthropic: Token-saving updates — cache-aware ITPM rate limits, simplified prompt caching, token-efficient tool use beta header |
| https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/overview | Anthropic: MCP Tunnels (research preview) — outbound-encrypted gateway for reaching private-network MCP servers without inbound firewall rules; relevant for future Managed Agents CI/CD path |
| https://www.anthropic.com/research/claude-code-expertise | Anthropic: "Agentic coding and persistent returns to expertise" — ~400k Claude Code session study; humans plan, Claude executes; every occupation succeeds at roughly equal rates; validates human-plans/agent-executes architecture |
| https://www.anthropic.com/engineering/building-c-compiler | Anthropic: Agent Teams in Claude Code (research preview) — 16 parallel agents write 100k-line Rust C compiler; validates code-reviewer multi-specialist dispatch pattern; watch for GA |
| https://docs.anthropic.com/en/release-notes/api | Anthropic: API Release Notes — track Managed Agents beta (memory, multi-agent sessions + Outcomes, self-hosted sandboxes, dynamic MCP config, large-output spillover >100K tokens) |
| https://www.anthropic.com/research/attack-navigator | Anthropic: LLM ATT&CK Navigator — 13,873 AI-enabled cyber threat observations mapped to MITRE ATT&CK v18 + ARiES scoring; reference for security-engineer agent threat modeling |
| https://red.anthropic.com/2026/attack-navigator/ | Anthropic Frontier Red Team: LLM ATT&CK Navigator interactive tool |
| https://www.anthropic.com/engineering/code-execution-with-mcp | Anthropic: Code Execution with MCP — presenting MCP servers as code APIs reduces token usage 98.7% (150k→2k tokens); pattern for MCP-heavy agent context optimization |
| https://www.anthropic.com/research/multiagent-systems | Anthropic Frontier Red Team: Patterns and problems in emerging multiagent systems (Aug 13 2026) — lock-out risk in parallel dispatch, thoughtfulness/foresight coordination framework, parallelizable vulnerability-detection use case |
| https://www.anthropic.com/engineering/managed-agents | Anthropic: Scaling Managed Agents — brain/hands separation pattern; decouples planning model from execution environment; maps to RepairBoss/specialist-agent dispatch architecture |
| https://www.anthropic.com/news/fable-mythos-access | Anthropic: Fable 5 and Mythos 5 — suspended June 12 2026 by US export control; **restored globally July 1 2026** after Commerce lifted controls; Anthropic deployed >99%-blocking classifier against Amazon-identified jailbreak technique; Fable 5 evaluation can now resume on all platforms |
| https://www.anthropic.com/news/introducing-claude-tag | Anthropic: Claude Tag — @Claude as a Slack team member; multiplayer shared context per channel; Enterprise/Team beta; complements CLI workflow for teams on Slack |
| https://www.anthropic.com/engineering/claude-code-auto-mode | Anthropic: How we built Claude Code auto mode — two-layer defense (read classifier + output transcript classifier on Sonnet 4.6); validates ag3nts permissions.defaultMode: "auto" design; 93% of prompts auto-approved |
