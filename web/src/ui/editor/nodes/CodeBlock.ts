import { textblockTypeInputRule } from "prosemirror-inputrules";
import { setBlockType } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import Node from "../core/Node";
import { toggleBlockType } from "../core/commands";
import type { ExtensionTypeOptions } from "../core/types";

export default class CodeBlock extends Node {
  override get name(): string {
    return "code_block";
  }

  override get markdownToken(): string {
    return "code_block";
  }

  override get schema(): NodeSpec {
    return {
      content: "text*",
      group: "block",
      code: true,
      defining: true,
      marks: "",
      parseDOM: [{ tag: "pre", preserveWhitespace: "full" }],
      toDOM: () => ["pre", ["code", 0]],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [textblockTypeInputRule(/^```$/, type)];
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    const paragraph = options.schema.nodes.paragraph;
    if (!type || !paragraph) return undefined;
    return toggleBlockType(type, paragraph);
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as NodeType | undefined;
    const paragraph = options.schema.nodes.paragraph;
    if (!type || !paragraph) return {};

    const backspace: Command = (state, dispatch) => {
      const { selection } = state;
      if (!selection.empty) return false;
      const { $from } = selection;
      if ($from.parent.type !== type) return false;
      if ($from.parentOffset !== 0 || $from.parent.content.size > 0) return false;
      setBlockType(paragraph)(state, dispatch);
      return true;
    };

    return { Backspace: backspace };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    const backticks = node.textContent.match(/`{3,}/gm);
    const fence = backticks ? backticks.sort().slice(-1)[0] + "`" : "```";
    state.write(fence + "\n");
    state.text(node.textContent, false);
    state.write("\n");
    state.write(fence);
    state.closeBlock(node);
  }

  override parseMarkdown() {
    return { block: "code_block", noCloseToken: true } as const;
  }
}
