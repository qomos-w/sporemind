import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import Node from "../core/Node";
import { renderCellContent } from "./Table";

export default class TableHeader extends Node {
  override get name(): string {
    return "table_header";
  }

  override get markdownToken(): string {
    return "th";
  }

  override get schema(): NodeSpec {
    return {
      content: "block+",
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      },
      tableRole: "header_cell",
      isolating: true,
      parseDOM: [{ tag: "th" }],
      toDOM: () => ["th", 0],
    };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    renderCellContent(state, node);
  }

  override parseMarkdown() {
    return { block: "table_header" } as const;
  }
}
