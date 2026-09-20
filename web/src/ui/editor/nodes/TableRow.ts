import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import Node from "../core/Node";

export default class TableRow extends Node {
  override get name(): string {
    return "table_row";
  }

  override get markdownToken(): string {
    return "tr";
  }

  override get schema(): NodeSpec {
    return {
      content: "(table_cell | table_header)*",
      tableRole: "row",
      parseDOM: [{ tag: "tr" }],
      toDOM: () => ["tr", 0],
    };
  }

  override toMarkdown(_state: MarkdownSerializerState, _node: ProsemirrorNode): void {
    // Serialization is handled by the parent table node.
  }

  override parseMarkdown() {
    return { block: "table_row" } as const;
  }
}
