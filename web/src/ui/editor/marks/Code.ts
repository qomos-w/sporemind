import { toggleMark } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkSpec, MarkType } from "prosemirror-model";
import Mark from "../core/Mark";
import type { ExtensionTypeOptions } from "../core/types";
import { markInputRule } from "../core/markInputRule";

export default class Code extends Mark {
  override get name(): string {
    return "code";
  }

  override get schema(): MarkSpec {
    return {
      parseDOM: [{ tag: "code" }],
      toDOM: () => ["code", 0],
      excludes: "_",
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? [markInputRule("`", type)] : [];
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as MarkType | undefined;
    return type ? { "Mod-e": toggleMark(type), "Mod-E": toggleMark(type) } : {};
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? () => toggleMark(type) : undefined;
  }

  override toMarkdown() {
    return { open: "`", close: "`", escape: false };
  }

  override parseMarkdown() {
    return { mark: "code", noCloseToken: true } as const;
  }

  override get markdownToken(): string {
    return "code_inline";
  }
}
