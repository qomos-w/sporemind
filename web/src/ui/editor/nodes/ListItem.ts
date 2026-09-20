import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import Node from "../core/Node";

export default class ListItem extends Node {
  override get name(): string {
    return "list_item";
  }

  override get schema(): NodeSpec {
    return {
      content: "block+",
      defining: true,
      parseDOM: [{ tag: "li" }],
      toDOM: () => ["li", 0],
    };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.renderContent(node);
  }

  override parseMarkdown() {
    return { block: "list_item" } as const;
  }
}
