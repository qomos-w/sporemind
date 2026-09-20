import type { PluginSimple } from "markdown-it";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, NodeType } from "prosemirror-model";
import { wrappingInputRule } from "prosemirror-inputrules";
import Node from "../core/Node";
import type { ExtensionTypeOptions } from "../core/types";

/**
 * Detects GFM task list syntax (`- [ ]` / `- [x]`) during markdown-it's core pass and
 * renames the affected `bullet_list`/`list_item` tokens to `checkbox_list`/`checkbox_item`,
 * stripping the `[ ]`/`[x]` marker from the inline content and carrying a `checked` attr.
 */
const taskListRule: PluginSimple = (md) => {
  md.core.ruler.after("block", "task_list", (state) => {
    const tokens = state.tokens;
    const taskItems = new Map<number, boolean>();

    for (let i = 0; i < tokens.length; i++) {
      if (tokens[i]!.type !== "list_item_open" || tokens[i]!.markup !== "-") continue;
      if (tokens[i + 1]?.type !== "paragraph_open") continue;
      const inlineTok = tokens[i + 2];
      if (inlineTok?.type !== "inline") continue;

      const match = inlineTok.content.match(/^\[( |x|X)\]\s+/);
      if (!match) continue;

      const checked = match[1] !== " ";
      taskItems.set(i, checked);

      const stripped = inlineTok.content.slice(match[0].length);
      inlineTok.content = stripped;
      if (inlineTok.children?.length) {
        inlineTok.children[0]!.content = stripped;
      }
    }

    if (taskItems.size === 0) return;

    for (let i = 0; i < tokens.length; i++) {
      if (tokens[i]!.type !== "bullet_list_open") continue;

      let depth = 1;
      let j = i + 1;
      let hasTask = false;

      while (j < tokens.length && depth > 0) {
        if (tokens[j]!.type === "bullet_list_open") depth++;
        else if (tokens[j]!.type === "bullet_list_close") depth--;
        else if (tokens[j]!.type === "list_item_open" && taskItems.has(j)) hasTask = true;
        j++;
      }
      j--;

      if (!hasTask) continue;

      tokens[i]!.type = "checkbox_list_open";
      tokens[i]!.tag = "ul";
      tokens[j]!.type = "checkbox_list_close";
      tokens[j]!.tag = "ul";

      for (let k = i + 1; k < j; k++) {
        if (tokens[k]!.type === "list_item_open") {
          tokens[k]!.type = "checkbox_item_open";
          tokens[k]!.tag = "li";
          if (taskItems.has(k)) {
            tokens[k]!.attrSet("checked", String(taskItems.get(k)));
          }
        } else if (tokens[k]!.type === "list_item_close") {
          tokens[k]!.type = "checkbox_item_close";
          tokens[k]!.tag = "li";
        }
      }
    }
  });
};

export default class CheckboxList extends Node {
  override get name(): string {
    return "checkbox_list";
  }

  override get rulePlugins(): PluginSimple[] {
    return [taskListRule];
  }

  override get schema(): NodeSpec {
    return {
      content: "checkbox_item+",
      group: "block",
      parseDOM: [{ tag: "ul.checkbox-list" }],
      toDOM: () => ["ul", { class: "checkbox-list" }, 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as NodeType | undefined;
    if (!type) return [];
    return [wrappingInputRule(/^\s*\[( |x|X)\]\s$/, type)];
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.renderList(node, "  ", (i) => {
      const child = node.child(i);
      return child.attrs.checked ? "- [x] " : "- [ ] ";
    });
  }

  override parseMarkdown() {
    return { block: "checkbox_list" } as const;
  }
}
