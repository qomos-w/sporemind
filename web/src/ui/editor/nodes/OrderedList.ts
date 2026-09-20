import type { MarkdownSerializerState } from "prosemirror-markdown";
import type Token from "markdown-it/lib/token.mjs";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { wrappingInputRule } from "prosemirror-inputrules";
import Node from "../core/Node";
import type { ExtensionTypeOptions } from "../core/types";

export default class OrderedList extends Node {
  override get name(): string {
    return "ordered_list";
  }

  override get schema(): NodeSpec {
    return {
      content: "list_item+",
      group: "block",
      attrs: { order: { default: 1, validate: "number" } },
      parseDOM: [
        {
          tag: "ol",
          getAttrs: (dom) => ({
            order: (dom as HTMLElement).hasAttribute("start")
              ? Number((dom as HTMLElement).getAttribute("start"))
              : 1,
          }),
        },
      ],
      toDOM: (node) => ["ol", { start: node.attrs.order === 1 ? null : node.attrs.order }, 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [
      wrappingInputRule(
        /^\s*(\d+)\.\s$/,
        type,
        (match) => ({ order: Number(match[1]) }),
        (match, node) => node.childCount + node.attrs.order === Number(match[1]),
      ),
    ];
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    const start = node.attrs.order ?? 1;
    const maxW = String(start + node.childCount - 1).length;
    const space = state.repeat(" ", maxW + 2);
    state.renderList(node, space, (i) => {
      const nStr = String(start + i);
      return state.repeat(" ", maxW - nStr.length) + nStr + ". ";
    });
  }

  override parseMarkdown() {
    return { block: "ordered_list", getAttrs: (tok: Token) => ({ order: Number(tok.attrGet("start")) || 1 }) } as const;
  }
}
