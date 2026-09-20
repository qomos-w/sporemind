import type { PluginSimple } from "markdown-it";
import type StateInline from "markdown-it/lib/rules_inline/state_inline.mjs";
import { toggleMark } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkSpec, MarkType } from "prosemirror-model";
import Mark from "../core/Mark";
import type { ExtensionTypeOptions } from "../core/types";
import { markInputRule } from "../core/markInputRule";

/**
 * Registers a `~~text~~` inline rule with markdown-it. This syntax is NOT part of
 * CommonMark, so it must be added as a custom rule — the same mechanism future inline
 * extensions (e.g. wikiword `[[…]]`) will use.
 */
const strikethroughRule: PluginSimple = (md) => {
  md.inline.ruler.before("emphasis", "s", (state: StateInline, silent) => {
    const max = state.posMax;
    const start = state.pos;
    if (state.src.charCodeAt(start) !== 0x7e /* ~ */ || state.src.charCodeAt(start + 1) !== 0x7e) {
      return false;
    }
    if (silent) return false;

    const marker = 2;
    let pos = start + marker;
    // Find the closing delimiter
    let close = -1;
    while (pos + 1 < max) {
      if (state.src.charCodeAt(pos) === 0x7e && state.src.charCodeAt(pos + 1) === 0x7e) {
        close = pos;
        break;
      }
      pos++;
    }
    if (close === -1) return false;

    const content = state.src.slice(start + marker, close);
    if (!content.trim()) return false;

    const tokenOpen = state.push("s_open", "s", 1);
    tokenOpen.markup = "~~";
    const tokenText = state.push("text", "", 0);
    tokenText.content = content;
    const tokenClose = state.push("s_close", "s", -1);
    tokenClose.markup = "~~";

    state.pos = close + marker;
    return true;
  });
};

export default class Strikethrough extends Mark {
  override get name(): string {
    return "s";
  }

  override get rulePlugins(): PluginSimple[] {
    return [strikethroughRule];
  }

  override get schema(): MarkSpec {
    return {
      parseDOM: [{ tag: "del" }, { tag: "s" }, { tag: "strike" }],
      toDOM: () => ["del", 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? [markInputRule("~~", type)] : [];
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as MarkType | undefined;
    return type ? { "Mod-Shift-x": toggleMark(type), "Mod-Shift-X": toggleMark(type) } : {};
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? () => toggleMark(type) : undefined;
  }

  override toMarkdown() {
    return { open: "~~", close: "~~", mixable: true, expelEnclosingWhitespace: true };
  }

  override parseMarkdown() {
    return { mark: "s" } as const;
  }
}
