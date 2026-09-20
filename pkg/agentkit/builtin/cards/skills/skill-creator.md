---
id: skill:skill-creator
type: skill
name: skill-creator
description: Create, edit, and explain Sporemind skills. Use whenever the user wants to author a new skill, modify an existing SKILL.md, understand the skill format, or bootstrap a skill from a description.
when_to_use: |
  - The user says "create a skill", "make a skill", "write a skill", "new skill", "skill-creator", "bootstrap a skill".
  - The user asks to modify or review an existing skill file.
  - The user wants to understand how skills work (mounting, frontmatter, body, context, allowed-tools).
  - The user describes a reusable workflow and doesn't realize it should be a skill.
arguments:
  - name
  - description
  - body
  - allowed_tools
  - context
---

# Skill Creator

Help users write, review, and improve Sporemind skills.

## What is a Skill?

A **skill** is a reusable prompt fragment that can be injected into an agent's hot context at runtime. Skills are stored as `SKILL.md` files inside a directory under a skills root.

Skills enable:
- **Reusable workflows**: Commit a workflow once, use it in any conversation.
- **Scoped tool access**: Limit which tools the agent sees while the skill is mounted.
- **Parameterized behavior**: Pass arguments into the skill body via template substitution.
- **Forked isolation**: Run a skill in a child agent to avoid polluting the parent context.

## File Location

Skills are discovered from these roots (in order of precedence, later overrides earlier by ID):

1. **Project-level**: `<projectRoot>/.sporecode/skills/<name>/SKILL.md`
2. **Global**: `~/.config/sporemind/skills/<name>/SKILL.md`
3. **Builtin**: Embedded in the application binary (read-only, overridden by user skills of the same ID).

## File Format

Each `SKILL.md` has two sections: YAML frontmatter + Markdown body.

```markdown
---
name: skill-id
description: One-line description. Also used to decide when to use this skill.
when_to_use: Optional. Detailed trigger conditions for the agent.
allowed-tools:
  - filesystem.read
  - filesystem.write
arguments:
  - target
  - format
context: inline      # or "fork"
---

# Skill Title

Markdown body. The agent will see this as a system instruction while the skill is mounted.
```

### Frontmatter Fields

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Unique ID. Used as `skillId` in `agent_skill_mount`. Also the directory name. |
| `description` | Yes | One-line summary. The agent sees this in the "Available skills" list to decide whether to mount it. |
| `when_to_use` | No | Extra trigger conditions. Use when the description alone is ambiguous. |
| `allowed-tools` | No | Array of tool names. When mounted, the agent's tool set is intersected with this list (plus a few always-available tools like `agent_skill_unmount`). If omitted, all tools remain available. |
| `arguments` | No | Array of parameter names. The skill can reference them via `${argName}` in the body. |
| `context` | No | `inline` (default) — body is injected into the current agent's hot context. `fork` — body is executed in a temporary child agent. |
| `argument-hint` | No | Human-readable hint shown in the UI when mounting a parameterized skill. |

### Body Template Syntax

The body is plain Markdown. Two substitution patterns are supported:

- `${args}` or `$args` — the entire argument string passed at mount time.
- `${paramName}` or `$paramName` — when `arguments` has exactly one entry, substitute that parameter.

Example:
```markdown
---
name: refactor
arguments: [language]
---

Refactor the code to follow ${language} best practices.
```

Mounting with `{"SkillId": "refactor", "Args": "Go"}` produces: `Refactor the code to follow Go best practices.`

### Context Modes

- **inline**: The skill body is appended to the current agent's system prompt. Fast, zero overhead, but increases token usage of the parent turn.
- **fork**: The skill body is sent to a child agent. The parent agent sees the child as a single tool call. Use for heavy, isolated, or side-effect-heavy tasks. The child inherits the parent's mounted skills but can have its own `allowed-tools` restriction.

## Workflow: Creating a New Skill

When the user asks to create a skill:

### Step 1: Gather Requirements

Ask the user (or infer from context):
1. **Name** — short, kebab-case ID (e.g., `git-commit`, `read-logs`).
2. **Description** — one line, including trigger keywords so the agent knows when to mount it.
3. **When to use** — specific utterances or situations that should trigger this skill.
4. **Body** — the actual instruction, workflow, or reference material.
5. **Arguments** — does the skill need parameters? If yes, list them and add an `argument-hint`.
6. **Allowed tools** — should the skill restrict the agent's tools? List only the ones needed.
7. **Context** — is this a lightweight inline helper, or a heavy isolated workflow? Default to `inline` unless there's a clear reason to fork.

### Step 2: Write the SKILL.md

Place the file at the appropriate location:
- If this is a **project-specific** skill: `<projectRoot>/.sporecode/skills/<name>/SKILL.md`
- If this is a **personal reusable** skill: `~/.config/sporemind/skills/<name>/SKILL.md`

Use `filesystem.write` to create the file. Ensure the directory exists first.

### Step 3: Validate

After writing, read the file back and verify:
- Frontmatter is valid YAML.
- `name` matches the directory name.
- `description` is concise and includes trigger keywords.
- Body contains no `${args}` if `arguments` is empty, and vice versa.
- If `allowed-tools` is present, tool names match the project's actual tool registry.

## Common Patterns

### Simple inline helper (no arguments)
```markdown
---
name: english-commit
description: Write English git commit messages. Use when the user asks to commit, save changes, or mentions 提交.
---

Write English commit messages. No sign-off, no Co-authored-by.
Exclude changes matched by `.claude gitignore`.
```

### Parameterized skill
```markdown
---
name: generate-readme
description: Generate a README for a project. Use when the user asks for README, readme, or project documentation.
arguments: [language]
argument-hint: Target natural language, e.g. "en", "zh", "ja"
---

Generate a README.md in ${language}. Include:
- Project title and one-line description
- Installation instructions
- Usage example
- License placeholder
```

### Forked workflow with restricted tools
```markdown
---
name: security-audit
description: Run a security audit on the codebase. Use when the user mentions security, audit, vulnerability, or pen-test.
allowed-tools:
  - filesystem.read
  - inspect
  - shell.exec
context: fork
---

Perform a security audit. Look for:
- Hardcoded secrets or API keys
- SQL injection patterns
- Unsafe file operations
- Missing input validation
Report findings as a markdown table with Severity, File, Line, and Recommendation.
```

## Anti-Patterns

- **Don't** make a skill that is just a single generic prompt — it won't be distinguishable from the base agent behavior. Skills should encapsulate *specific* knowledge, constraints, or workflows.
- **Don't** use `allowed-tools` to allow *more* tools than the agent already has. It is a restriction list, not an expansion list.
- **Don't** put overly long descriptions in `description`. The "Available skills" list has a ~2KB character budget. Keep it tight.
- **Don't** omit `when_to_use` if the trigger is subtle. The agent relies on `description + when_to_use` to decide whether to mount.

## Meta: Improving This Skill

If you notice this skill-creator itself is missing guidance (e.g., a new frontmatter field, a new pattern, a new anti-pattern), propose an edit to the builtin `skill-creator/SKILL.md` or create a user-level override at `~/.config/sporemind/skills/skill-creator/SKILL.md`.
