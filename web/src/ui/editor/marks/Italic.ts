import { toggleMark } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkSpec, MarkType } from "prosemirror-model";
import Mark from "../core/Mark";
import type { ExtensionTypeOptions } from "../core/types";
import { markInputRule } from "../core/markInputRule";

export default class Italic extends Mark {
  override get name(): string {
    return "em";
  }

  override get schema(): MarkSpec {
    return {
      parseDOM: [{ tag: "i" }, { tag: "em" }, { style: "font-style=italic" }],
      toDOM: () => ["em", 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? [markInputRule("*", type)] : [];
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as MarkType | undefined;
    return type ? { "Mod-i": toggleMark(type), "Mod-I": toggleMark(type) } : {};
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? () => toggleMark(type) : undefined;
  }

  override toMarkdown() {
    return { open: "*", close: "*", mixable: true, expelEnclosingWhitespace: true };
  }

  override parseMarkdown() {
    return { mark: "em" } as const;
  }
}
