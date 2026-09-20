import { textblockTypeInputRule } from "prosemirror-inputrules";
import { setBlockType } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type Token from "markdown-it/lib/token.mjs";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import Node from "../core/Node";
import { toggleBlockType } from "../core/commands";
import type { ExtensionTypeOptions } from "../core/types";

export default class Heading extends Node {
  override get name(): string {
    return "heading";
  }

  override get schema(): NodeSpec {
    return {
      attrs: { level: { default: 1, validate: "number" } },
      content: "inline*",
      group: "block",
      defining: true,
      parseDOM: [
        { tag: "h1", attrs: { level: 1 } },
        { tag: "h2", attrs: { level: 2 } },
        { tag: "h3", attrs: { level: 3 } },
        { tag: "h4", attrs: { level: 4 } },
        { tag: "h5", attrs: { level: 5 } },
        { tag: "h6", attrs: { level: 6 } },
      ],
      toDOM: (node) => ["h" + node.attrs.level, 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [
      textblockTypeInputRule(/^(#{1,6})\s$/, type, (match) => ({
        level: match[1]!.length,
      })),
    ];
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as NodeType | undefined;
    const paragraph = options.schema.nodes["paragraph"];
    if (!type || !paragraph) return {};
    const h = (level: number) => toggleBlockType(type, paragraph, { level })();

    // Backspace at the start of an empty heading → revert to paragraph.
    const backspace: Command = (state, dispatch) => {
      const { selection } = state;
      if (!selection.empty) return false;
      const { $from } = selection;
      if ($from.parent.type !== type) return false;
      if ($from.parentOffset !== 0 || $from.parent.content.size > 0) return false;
      setBlockType(paragraph)(state, dispatch);
      return true;
    };

    return { "Mod-Alt-1": h(1), "Mod-Alt-2": h(2), "Mod-Alt-3": h(3), Backspace: backspace };
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    const paragraph = options.schema.nodes["paragraph"];
    if (!type || !paragraph) return undefined;
    return toggleBlockType(type, paragraph);
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.write(state.repeat("#", node.attrs.level) + " ");
    state.renderInline(node, false);
    state.closeBlock(node);
  }

  override parseMarkdown() {
    return { block: "heading", getAttrs: (tok: Token) => ({ level: Number(tok.tag.slice(1)) }) } as const;
  }
}
