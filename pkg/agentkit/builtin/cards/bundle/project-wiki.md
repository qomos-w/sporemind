---
id: builtin:bundle:project-wiki
type: bundle
title: Project Wiki
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: book-open
  visual:
    icon: book-open
    accent: green
    color: "#16a34a"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  settingsProtected: true
  tools:
    - project.wiki_list_cards
    - project.wiki_get_card
    - project.wiki_get_card_hierarchy
    - project.wiki_get_concept_tree
    - project.wiki_create_card
    - project.wiki_edit_card
    - project.wiki_delete_card
    - project.wiki_search_card_content
    - project.wiki_validate_card
---

Use project wiki tools to read and persist project knowledge.

## WikiWord Card Linking

You can create inline links to project wiki cards in your text output. These render as clickable links in card bodies, AI timeline text, and tool call results.

Supported syntaxes:
- [[CardId]] - explicit link to a card by ID or title. Use [[display text|CardId]] for custom display text.
- `[text](wiki:target)` - Markdown link to a card.
- CamelCase - words like ProjectSummary or ApiKeyManager are auto-linked.
- `CamelCase` - CamelCase in single backticks becomes a code-styled wiki link. Use plain backticks (for example, `file.go`) for project file references instead.

Use card links to reference knowledge the user can explore: project components, concepts, design decisions, or other cards.

## Tool Usage

- **Read**: `project.wiki_get_card`, `project.wiki_list_cards`, `project.wiki_get_concept_tree` to retrieve knowledge. `project.wiki_list_cards` supports two modes: tree mode (default) renders the hierarchy with ASCII Tree and structured Nodes; flat mode (`Flat=true`) returns a flat array with optional `Query`/`Tags`/`Status`/`Type` filters and `OrderBy` (default `-modified`). Filters and `Query` only apply in flat mode; `Query` matches card title/ID only (case-insensitive substring or `/regexp/`), not the body. Use `project.wiki_search_card_content` to find cards by **body content** (substring or `/regexp/`) when you need to locate which cards mention a concept or text in their prose; use `project.wiki_validate_card` to check raw markdown before persisting.
- **Write**: `project.wiki_create_card` for new cards; `project.wiki_edit_card` to edit existing ones. `project.wiki_edit_card` supports two modes: full raw replacement (`Raw`) or a frontmatter-aware body patch (`OldString`+`NewString`) that preserves the frontmatter and only replaces the matched fragment in the body; prefer the patch mode for targeted edits after reading the card, and fall back to full `Raw` when replacing most of the card. In patch mode, if `OldString` occurs more than once you must either narrow it to a single occurrence or set `ReplaceAll: true`; a no-op (`OldString`==`NewString`) is rejected. Workflow task cards — including deterministic `data.exec.kind: script` cards — are authored with the specialized `project.wiki_create_task_card`, whose optional `Exec` map writes a structured `data.exec` block; `project.wiki_create_card` with a full `Raw` is the fallback when the structured callable cannot express the card. Both paths run `project.wiki_validate_card` on save, so a malformed card never persists. See [[script-card-authoring]] for the exec contract, the `spore` fence / `run(inputs...)` entry, budget keys, capability whitelist, determinism contract, and worked examples.
- **Organize**: `project.wiki_list_cards` with `Flat=true` to discover cards.

## Naming

When creating or renaming a card, keep its `id` (display title written into frontmatter) and the filename slug in the **same language**. Mixing languages (e.g. an English slug with a Chinese display title) breaks wiki-word resolution and makes cards hard to find. If the display title is `对话模式搜索组件设计`, derive the filename slug from the same text rather than a foreign-language slug.

## Do Not

- Do not read or write wiki cards via shell/file tools.
- Do not delete protected cards (`builtin:*`, `__builtin_*__`).
- Do not dump raw card bodies into chat; summarize or quote selectively.
- Do not create empty cards; every card must have a meaningful title and body.
