# Recipe System

Recipes are declarative YAML files that bundle a task (or pipeline of tasks) with a persona, agent preference, parameters, and constraints. Drop a `.yaml` file in `config/recipes/` and it's available via `/recipe`.

## Single-task recipe

The simplest form — one agent, one prompt:

```yaml
name: research
description: Deep research on any topic via Gemini
agent: gemini
tags: [research]

parameters:
  - key: query
    type: string
    required: true
    description: The research question

system_prompt: |
  You are a research agent. Investigate the following topic thoroughly.
  Use web search to find current, authoritative sources.
  Query: {{query}}

timeout: 10m
```

Run it:
```
/recipe research query=what are the tradeoffs between SQLite WAL and rollback journal
```

## Multi-task recipe (pipeline)

A DAG of stages routed to different agents:

```yaml
name: repair-lite
description: Lightweight 4-stage pipeline

parameters:
  - key: objective
    type: string
    required: true
    min_words: 5
    description: What to build or fix (complete sentence)

tasks:
  - id: research
    agent: gemini
    type: repair.research
    prompt_template: |
      Research the following objective...
      Objective: {{objective}}
    timeout: 10m

  - id: plan
    agent: claude
    type: repair.plan
    depends_on: [research]
    context_from: [research]
    prompt_template: |
      Produce an implementation plan...
      Objective: {{objective}}
    timeout: 10m

  - id: implement
    agent: codex
    type: repair.implement
    depends_on: [plan]
    context_from: [plan]
    prompt_template: |
      Write code that implements the plan above.
      Objective: {{objective}}
    timeout: 20m

  - id: review
    agent: claude
    type: repair.review
    depends_on: [implement]
    context_from: [research, plan, implement]
    evaluator_of: implement
    evaluator_retries: 2
    prompt_template: |
      Review the implementation. First line must be:
        ACCEPT: <summary>
        REJECT: <reason>
        BLOCKED: <reason>
      Objective: {{objective}}
    timeout: 10m
```

## Parameters

```yaml
parameters:
  - key: objective
    type: string          # string, integer, number, boolean, file, select
    required: true
    default: ""           # applied when value is not provided
    description: "..."
    min_words: 5          # minimum word count (optional)
    min_chars: 20         # minimum character count (optional)
    pattern: "^[A-Z]"    # regex the value must match (optional)
    options: [a, b, c]   # for select type only
```

Validation runs before dispatch. Errors are immediate:
```
✘ recipe "repair-lite": parameter "objective" too vague: minimum 5 words, got 1 ("add")
```

## Evaluator loop

When a task has `evaluator_of: <stage>` and `evaluator_retries: N`, the review stage becomes an evaluator:

- **ACCEPT**: pipeline ends cleanly
- **REJECT**: spawns retry impl + retry eval (up to N times)
- **BLOCKED**: terminates immediately, marks impl as failed

The evaluator parses the first line of the review output. Fallback keyword search (first 2000 chars) catches models that don't follow the format exactly.

## Dry-run mode

Preview the expanded pipeline without dispatching:

```
/recipe repair-lite --dry-run objective=add a health check endpoint
```

Shows: parameters, stages with agents/deps/timeouts, prompt previews, and historical cost estimates from SQLite.

## Template features

### Parameter substitution
`{{objective}}` in prompt templates is replaced with the parameter value.

### File includes
```yaml
prompt_template: |
  {{#include:config/recipes/repair-prefixes/research-prefix.md}}
  {{#include:shared/claude-code/files/pipeline/research.md}}
  Target objective: {{objective}}
```

## Bundled recipes

| Recipe | Stages | Agents | Purpose |
|---|---|---|---|
| `repair.yaml` | 7 (discovery→review) | coordinator/gemini/claude/codex | Full REPAIR pipeline |
| `repair-lite.yaml` | 4 (research→review) | gemini/claude/codex | Quick iteration |
| `research.yaml` | 1 | gemini | Web research |
| `code-review.yaml` | 1 | claude | Code review |
| `repo-audit.yaml` | 1 | claude | Repository audit |
