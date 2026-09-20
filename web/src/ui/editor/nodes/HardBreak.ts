import type { Command } from "prosemirror-state";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { NodeSpec, NodeType } from "prosemirror-model";
import Node from "../core/Node";
import type { ExtensionTypeOptions } from "../core/types";

export default class HardBreak extends Node {
  override get name(): string {
    return "br";
  }

  override get schema(): NodeSpec {
    return {
      inline: true,
      group: "inline",
      selectable: false,
      parseDOM: [{ tag: "br" }],
      toDOM: () => ["br"],
      leafText: () => "\n",
    };
  }

  override get markdownToken(): string {
    return "hardbreak";
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as NodeType | undefined;
    if (!type) return {};
    return {
      "Shift-Enter": (state, dispatch) => {
        dispatch?.(state.tr.replaceSelectionWith(type.create()).scrollIntoView());
        return true;
      },
    };
  }

  // The serializer writes hard breaks automatically via the `hardBreakNodeName`
  // option, so this is only reached if the node is serialized out of inline context.
  override toMarkdown(state: MarkdownSerializerState): void {
    state.write("\\\n");
  }

  override parseMarkdown() {
    return { node: "br" } as const;
  }
}
