# RichCardEditor — Extension Guide

The editor is built on ProseMirror with a lightweight extension system inspired by
[Outline](https://github.com/outline/outline). Every node, mark, and embedded component
is an `Extension` subclass that self-describes its schema, markdown serialization, keyboard
shortcuts, input rules, and (optionally) a React component for rendering.

## Architecture

```
Extension (base)
├── Node          — block or inline node (schema + toMarkdown + parseMarkdown + keys + inputRules)
│   └── ReactNode — Node that renders a React component via ProseMirror NodeView
└── Mark          — inline formatting (schema + toMarkdown + parseMarkdown + keys)
```

`ExtensionManager` collects all extensions, builds a ProseMirror `Schema`, a markdown
`Parser`/`Serializer`, keymaps, input rules, commands, and NodeView constructors.

## Presets

| Preset | Includes |
|--------|----------|
| `inlineExtensions` | Doc, Paragraph, Text, HardBreak, Bold, Italic, Code, Strikethrough, Link, WikiWord, CardMention |
| `basicExtensions` | inlineExtensions + Heading, Blockquote, HR, CodeBlock, BulletList, OrderedList, ListItem, CheckboxList, CheckboxItem |
| `richExtensions` | basicExtensions + anything registered via `CardNodeRegistry` |

`RichCardEditor` defaults to `basicExtensions` + `CardNodeRegistry.getExtensions()`.

## Adding a custom inline component (ReactNode)

This is the full pattern — demonstrated by `WikiWord` and `CardMention`.

### 1. Create the extension

```tsx
// nodes/Timestamp.tsx
import type { PluginSimple } from "markdown-it";
import type StateInline from "markdown-it/lib/rules_inline/state_inline.mjs";
import type Token from "markdown-it/lib/token.mjs";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { InputRule } from "prosemirror-inputrules";
import ReactNode from "../core/ReactNode";
import type { ComponentProps, ExtensionTypeOptions, CommandFactory } from "../core/types";

// --- 1a. Custom markdown syntax: {{ts:1234567890}}
const timestampRule: PluginSimple = (md) => {
  md.inline.ruler.before("link", "timestamp", (state: StateInline, silent) => {
    const start = state.pos;
    if (!state.src.startsWith("{{ts:", start)) return false;
    const end = state.src.indexOf("}}", start + 5);
    if (end === -1) return false;
    const value = state.src.slice(start + 5, end);
    if (!/^\d+$/.test(value)) return false;
    if (!silent) {
      const tok = state.push("timestamp", "timestamp", 0);
      tok.content = value;
      tok.attrSet("value", value);
    }
    state.pos = end + 2;
    return true;
  });
};

// --- 1b. React component
function TimestampChip({ node }: ComponentProps): React.ReactElement {
  const date = new Date(Number(node.attrs.value));
  return (
    <span className="rce-timestamp" contentEditable={false}>
      {date.toLocaleDateString()}
    </span>
  );
}

// --- 1c. The extension class
export default class Timestamp extends ReactNode {
  override get name() { return "timestamp"; }
  override component = TimestampChip;
  override get rulePlugins(): PluginSimple[] { return [timestampRule]; }

  override get schema(): NodeSpec {
    return {
      inline: true,
      group: "inline",
      atom: true,
      attrs: { value: { default: "" } },
      toDOM: (node) => ["span", { class: "rce-timestamp", contentEditable: false }, new Date(Number(node.attrs.value)).toLocaleDateString()],
      leafText: (node) => node.attrs.value,
    };
  }

  // Live input rule: typing {{ts:123}} converts to chip
  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [new InputRule(/\{\{ts:(\d+)\}\}$/, (state, match, start, end) => {
      const value = match[1];
      if (!value) return null;
      const tr = state.tr.delete(start, end);
      tr.insert(start, type.create({ value }));
      return tr;
    })];
  }

  // Markdown round-trip
  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode) {
    state.write(`{{ts:${node.attrs.value}}}`);
  }
  override parseMarkdown() {
    return { node: "timestamp", getAttrs: (tok: Token) => ({ value: tok.attrGet("value") || tok.content || "" }) } as const;
  }
}
```

### 2. Register it

```ts
import { CardNodeRegistry, Timestamp } from "@/ui/editor";

CardNodeRegistry.register(Timestamp, {
  menuItem: { label: "Timestamp", keywords: "time date stamp" },
});
```

That's it. The node is now:
- Part of the editor schema
- Parsed from and serialized to markdown
- Has a slash-menu entry (type `/time` → insert)
- Typed live via input rule
- Rendered as a React chip via the NodeView bridge

## Adding a plain Node (non-React)

Same pattern but extend `Node` instead of `ReactNode`. No `component` property.
See `Heading.ts`, `CheckboxList.ts`, `Blockquote.ts` for examples.

## Adding a Mark

Extend `Mark`. Provide `schema` (with `toDOM`/`parseDOM`), `toMarkdown` (returns
`{ open, close, mixable }`), `parseMarkdown`, and optionally `keys`/`inputRules`.
See `Bold.ts`, `Strikethrough.ts` for examples.

## Key methods to implement

| Method | Purpose |
|--------|---------|
| `name` | Unique identifier (used as ProseMirror node/mark name) |
| `schema` | ProseMirror `NodeSpec` / `MarkSpec` |
| `rulePlugins` | markdown-it plugins for custom syntax (non-CommonMark) |
| `toMarkdown(state, node)` | Serialize to markdown |
| `parseMarkdown()` | Declare how markdown-it tokens map to this node/mark |
| `inputRules(options)` | ProseMirror input rules (auto-format on typing) |
| `keys(options)` | Keyboard shortcuts → ProseMirror Commands |
| `commands(options)` | Command factories for toolbar/menu invocation |
| `component` | (ReactNode only) React component for NodeView rendering |
