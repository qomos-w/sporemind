import type { PluginSimple } from "markdown-it";
import type StateInline from "markdown-it/lib/rules_inline/state_inline.mjs";
import type Token from "markdown-it/lib/token.mjs";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import { InputRule } from "prosemirror-inputrules";
import type { NodeSpec, NodeType, Node as ProsemirrorNode } from "prosemirror-model";
import ReactNode from "../core/ReactNode";
import type { ComponentProps, ExtensionTypeOptions, CommandFactory } from "../core/types";

/**
 * Registers a `[[WikiWord]]` or `[[display|target]]` inline rule with markdown-it.
 * The syntax becomes an inline node whose attrs carry the display text and target.
 */
const wikiwordRule: PluginSimple = (md) => {
  md.inline.ruler.before("link", "wikiword", (state: StateInline, silent) => {
    const start = state.pos;
    if (state.src.charCodeAt(start) !== 0x5b /* [ */ || state.src.charCodeAt(start + 1) !== 0x5b) {
      return false;
    }
    const close = state.src.indexOf("]]", start + 2);
    if (close === -1) return false;

    const raw = state.src.slice(start + 2, close).trim();
    if (!raw) return false;

    const pipe = raw.indexOf("|");
    const display = pipe >= 0 ? raw.slice(0, pipe).trim() : raw;
    const target = pipe >= 0 ? raw.slice(pipe + 1).trim() : raw;
    if (!display || !target) return false;

    if (!silent) {
      const token = state.push("wikiword", "wikiword", 0);
      token.markup = "[[]]";
      token.content = raw;
      token.attrSet("display", display);
      token.attrSet("target", target);
    }
    state.pos = close + 2;
    return true;
  });
};

/**
 * Registers a `[display](wiki:target)` inline rule with markdown-it, before the
 * standard `link` rule so wiki-protocol links become wikiword nodes instead of
 * plain links.
 */
const wikiwordMarkdownLinkRule: PluginSimple = (md) => {
  md.inline.ruler.before("link", "wikiword_md", (state: StateInline, silent) => {
    const start = state.pos;
    if (state.src.charCodeAt(start) !== 0x5b /* [ */) {
      return false;
    }
    const match = state.src.slice(start).match(/^\[([^\]]*)\]\(([^)]+)\)/);
    if (!match) return false;

    const display = (match[1] ?? '').trim();
    const url = (match[2] ?? '').trim();
    if (!url.startsWith("wiki:")) return false;

    const target = url.slice(5).trim() || display;
    if (!display || !target) return false;

    if (!silent) {
      const token = state.push("wikiword", "wikiword", 0);
      token.markup = "[]()";
      token.content = `${display}|${target}`;
      token.attrSet("display", display);
      token.attrSet("target", target);
    }
    state.pos += match[0].length;
    return true;
  });
};

function WikiwordChip({ node, host }: ComponentProps): React.ReactElement {
  const display = String(node.attrs.display);
  const target = String(node.attrs.target);
  const onWikiWordClick = host?.onWikiWordClick as ((word: string) => void) | undefined;
  return (
    <span
      className="rce-wikiword"
      onMouseDown={(e) => {
        e.preventDefault();
        onWikiWordClick?.(target);
      }}
    >
      {display}
    </span>
  );
}

export default class WikiWord extends ReactNode {
  override get name(): string {
    return "wikiword";
  }

  override component = WikiwordChip;

  override get rulePlugins(): PluginSimple[] {
    return [wikiwordRule, wikiwordMarkdownLinkRule];
  }

  override get schema(): NodeSpec {
    return {
      inline: true,
      group: "inline",
      atom: true,
      attrs: {
        display: { default: "" },
        target: { default: "" },
      },
      selectable: true,
      draggable: false,
      parseDOM: [
        {
          tag: "span[data-wikiword]",
          getAttrs: (dom) => {
            const el = dom as HTMLElement;
            const display = el.getAttribute("data-display") || el.getAttribute("data-wikiword") || "";
            const target = el.getAttribute("data-target") || display;
            return { display, target };
          },
        },
      ],
      toDOM: (node) => [
        "span",
        { "data-wikiword": node.attrs.target, "data-display": node.attrs.display, "data-target": node.attrs.target, class: "rce-wikiword" },
        node.attrs.display,
      ],
      leafText: (node) => node.attrs.display,
    };
  }

  override commands(options: ExtensionTypeOptions): CommandFactory | undefined {
    const type = options.type as NodeType | undefined;
    if (!type) return undefined;
    return (attrs) => (state, dispatch) => {
      const a = attrs as { display?: string; target?: string } | undefined;
      const display = a?.display || "";
      const target = a?.target || display;
      if (!display) return false;
      const node = type.create({ display, target });
      dispatch?.(state.tr.replaceSelectionWith(node).scrollIntoView());
      return true;
    };
  }

  override inputRules(options: ExtensionTypeOptions): InputRule[] {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    // When the user types `]]` after `[[...]]`, convert the raw text into a wikiword node.
    // Supports `[[word]]` and `[[display|target]]`.
    return [
      new InputRule(/\[\[(.+?)]\]$/, (state, match, start, end) => {
        const raw = match[1];
        if (!raw) return null;
        const pipe = raw.indexOf("|");
        const display = pipe >= 0 ? raw.slice(0, pipe).trim() : raw;
        const target = pipe >= 0 ? raw.slice(pipe + 1).trim() : raw;
        if (!display || !target) return null;
        const tr = state.tr.delete(start, end);
        tr.insert(start, type.create({ display, target }));
        return tr;
      }),
      // When the user types `)` to close `[display](wiki:target)`, convert it
      // into a wikiword node.
      new InputRule(/\[([^\]]+)\]\(wiki:([^)]+)\)$/, (state, match, start, end) => {
        const display = (match[1] ?? '').trim();
        const target = (match[2] ?? '').trim();
        if (!display || !target) return null;
        const tr = state.tr.delete(start, end);
        tr.insert(start, type.create({ display, target }));
        return tr;
      }),
    ];
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    const display = String(node.attrs.display);
    const target = String(node.attrs.target);
    if (display === target) {
      state.write(`[[${display}]]`);
    } else {
      state.write(`[[${display}|${target}]]`);
    }
  }

  override parseMarkdown() {
    return {
      node: "wikiword",
      getAttrs: (tok: Token) => {
        const display = tok.attrGet("display") || tok.content || "";
        const target = tok.attrGet("target") || display;
        return { display, target };
      },
    } as const;
  }
}
