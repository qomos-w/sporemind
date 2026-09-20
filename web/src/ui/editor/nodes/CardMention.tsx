import type { PluginSimple } from "markdown-it";
import type StateInline from "markdown-it/lib/rules_inline/state_inline.mjs";
import type Token from "markdown-it/lib/token.mjs";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { InputRule } from "prosemirror-inputrules";
import ReactNode from "../core/ReactNode";
import type { ComponentProps, ExtensionTypeOptions, CommandFactory } from "../core/types";

/**
 * Registers an `@[title](card://cardId)` inline rule with markdown-it. This is a
 * distinct syntax from regular links — the `@` prefix + `card://` protocol mark it
 * as a card reference (CardMention), rendered as an interactive chip by React.
 */
const cardMentionRule: PluginSimple = (md) => {
  md.inline.ruler.before("link", "card_mention", (state: StateInline, silent) => {
    const start = state.pos;
    if (state.src.charCodeAt(start) !== 0x40 /* @ */ || state.src.charCodeAt(start + 1) !== 0x5b /* [ */) {
      return false;
    }
    const closeBracket = state.src.indexOf("]", start + 2);
    if (closeBracket === -1 || state.src.charCodeAt(closeBracket + 1) !== 0x28 /* ( */) return false;
    const closeParen = state.src.indexOf(")", closeBracket + 2);
    if (closeParen === -1) return false;

    const title = state.src.slice(start + 2, closeBracket).trim();
    const url = state.src.slice(closeBracket + 2, closeParen).trim();
    if (!url.startsWith("card://")) return false;
    const cardId = url.slice("card://".length);
    if (!cardId || !title) return false;

    if (!silent) {
      const token = state.push("card_mention", "card_mention", 0);
      token.markup = "@[](card://)";
      token.content = title;
      token.attrSet("cardId", cardId);
      token.attrSet("title", title);
    }
    state.pos = closeParen + 1;
    return true;
  });
};

function CardMentionChip({ node }: ComponentProps): React.ReactElement {
  const { cardId, title } = node.attrs as { cardId: string; title: string };
  return (
    <span
      className="rce-card-mention"
      contentEditable={false}
      title={`card://${cardId}`}
    >
      <svg width="11" height="11" viewBox="0 0 16 16" fill="none" style={{ flexShrink: 0 }}>
        <path d="M4 3.5A1.5 1.5 0 015.5 2h5A1.5 1.5 0 0112 3.5v9a.5.5 0 01-.8.4L8 10.5l-3.2 2.4a.5.5 0 01-.8-.4v-9z" fill="currentColor" />
      </svg>
      {title}
    </span>
  );
}

export default class CardMention extends ReactNode {
  override get name(): string {
    return "card_mention";
  }

  override component = CardMentionChip;

  override get rulePlugins(): PluginSimple[] {
    return [cardMentionRule];
  }

  override get schema(): NodeSpec {
    return {
      inline: true,
      group: "inline",
      atom: true,
      attrs: {
        cardId: { default: "" },
        title: { default: "" },
      },
      selectable: true,
      parseDOM: [
        {
          tag: "span[data-card-id]",
          getAttrs: (dom) => ({
            cardId: (dom as HTMLElement).getAttribute("data-card-id") || "",
            title: (dom as HTMLElement).getAttribute("data-card-title") || "",
          }),
        },
      ],
      toDOM: (node) => [
        "span",
        { "data-card-id": node.attrs.cardId, "data-card-title": node.attrs.title, class: "rce-card-mention", contentEditable: false },
        node.attrs.title,
      ],
      leafText: (node) => node.attrs.title,
    };
  }

  override commands(options: ExtensionTypeOptions): CommandFactory | undefined {
    const type = options.type as NodeType | undefined;
    if (!type) return undefined;
    return (attrs) => (state, dispatch) => {
      const cardId = (attrs as { cardId?: string } | undefined)?.cardId;
      const title = (attrs as { title?: string } | undefined)?.title;
      if (!cardId || !title) return false;
      const node = type.create({ cardId, title });
      dispatch?.(state.tr.replaceSelectionWith(node).scrollIntoView());
      return true;
    };
  }

  override inputRules(options: ExtensionTypeOptions): InputRule[] {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [
      new InputRule(/@\[([^\]]+)\]\(card:\/\/([^)]+)\)$/, (state, match, start, end) => {
        const title = match[1];
        const cardId = match[2];
        if (!title || !cardId) return null;
        const tr = state.tr.delete(start, end);
        tr.insert(start, type.create({ title, cardId }));
        return tr;
      }),
    ];
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.write(`@[${String(node.attrs.title)}](card://${String(node.attrs.cardId)})`);
  }

  override parseMarkdown() {
    return {
      node: "card_mention",
      getAttrs: (tok: Token) => ({
        cardId: tok.attrGet("cardId") || "",
        title: tok.attrGet("title") || tok.content || "",
      }),
    } as const;
  }
}
