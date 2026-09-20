import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { InputRule } from "prosemirror-inputrules";
import Node from "../core/Node";
import type { CommandFactory, ExtensionTypeOptions } from "../core/types";

export default class HorizontalRule extends Node {
  override get name(): string {
    return "horizontal_rule";
  }

  override get schema(): NodeSpec {
    return {
      group: "block",
      parseDOM: [{ tag: "hr" }],
      toDOM: () => ["hr"],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    // Replace a line containing only --- with a horizontal rule node.
    return [
      new InputRule(/^---\s$/, (state, _match, start, end) => {
        const tr = state.tr.delete(start, end);
        tr.replaceRangeWith(start, start, type.create());
        return tr;
      }),
    ];
  }

  override commands(options: ExtensionTypeOptions): CommandFactory | undefined {
    const type = options.type as NodeType | undefined;
    if (!type) return undefined;
    return () => (state, dispatch): boolean => {
      const { $from, $to } = state.selection;
      const range = $from.blockRange($to);
      if (!range) return false;
      const tr = state.tr.replaceRangeWith(range.start, range.end, type.create());
      dispatch?.(tr.scrollIntoView());
      return true;
    };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.write("---");
    state.closeBlock(node);
  }

  override parseMarkdown() {
    return { node: "horizontal_rule" } as const;
  }
}

