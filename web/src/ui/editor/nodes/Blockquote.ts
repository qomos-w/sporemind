import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { wrappingInputRule } from "prosemirror-inputrules";
import Node from "../core/Node";
import { toggleWrap } from "../core/commands";
import type { ExtensionTypeOptions } from "../core/types";

export default class Blockquote extends Node {
  override get name(): string {
    return "blockquote";
  }

  override get schema(): NodeSpec {
    return {
      content: "block+",
      group: "block",
      defining: true,
      parseDOM: [{ tag: "blockquote" }],
      toDOM: () => ["blockquote", 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [wrappingInputRule(/^\s*>\s$/, type)];
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return undefined;
    return toggleWrap(type);
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.wrapBlock("> ", null, node, () => state.renderContent(node));
  }

  override parseMarkdown() {
    return { block: "blockquote" } as const;
  }
}
