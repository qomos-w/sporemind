import { setBlockType } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import Node from "../core/Node";
import type { ExtensionTypeOptions } from "../core/types";

export default class Paragraph extends Node {
  override get name(): string {
    return "paragraph";
  }

  override get schema(): NodeSpec {
    return {
      content: "inline*",
      group: "block",
      parseDOM: [{ tag: "p" }],
      toDOM: () => ["p", { dir: "auto" }, 0],
    };
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as NodeType | undefined;
    if (!type) return {};
    return { "Shift-Ctrl-0": setBlockType(type) };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.renderInline(node);
    state.closeBlock(node);
  }

  override parseMarkdown() {
    return { block: "paragraph" } as const;
  }
}
