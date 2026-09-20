import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { wrappingInputRule } from "prosemirror-inputrules";
import Node from "../core/Node";
import type { ExtensionTypeOptions } from "../core/types";

export default class BulletList extends Node {
  override get name(): string {
    return "bullet_list";
  }

  override get schema(): NodeSpec {
    return {
      content: "list_item+",
      group: "block",
      parseDOM: [{ tag: "ul" }],
      toDOM: () => ["ul", 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [wrappingInputRule(/^\s*([-+*])\s$/, type)];
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.renderList(node, "  ", () => "- ");
  }

  override parseMarkdown() {
    return { block: "bullet_list" } as const;
  }
}
