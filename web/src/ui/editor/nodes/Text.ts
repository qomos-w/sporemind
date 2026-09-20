import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import Node from "../core/Node";

export default class Text extends Node {
  override get name(): string {
    return "text";
  }

  override get schema(): NodeSpec {
    return { group: "inline" };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.text(node.text || "");
  }
}
