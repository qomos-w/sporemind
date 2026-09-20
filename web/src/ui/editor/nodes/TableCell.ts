import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import Node from "../core/Node";
import { renderCellContent } from "./Table";

export default class TableCell extends Node {
  override get name(): string {
    return "table_cell";
  }

  override get markdownToken(): string {
    return "td";
  }

  override get schema(): NodeSpec {
    return {
      content: "block+",
      attrs: {
        colspan: { default: 1 },
        rowspan: { default: 1 },
        colwidth: { default: null },
      },
      tableRole: "cell",
      isolating: true,
      parseDOM: [{ tag: "td" }],
      toDOM: () => ["td", 0],
    };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    renderCellContent(state, node);
  }

  override parseMarkdown() {
    return { block: "table_cell" } as const;
  }
}
