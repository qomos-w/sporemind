import { toggleMark } from "prosemirror-commands";
import type { Command } from "prosemirror-state";
import type { MarkSpec, MarkType } from "prosemirror-model";
import Mark from "../core/Mark";
import type { ExtensionTypeOptions } from "../core/types";
import { markInputRule } from "../core/markInputRule";

export default class Bold extends Mark {
  override get name(): string {
    return "strong";
  }

  override get schema(): MarkSpec {
    return {
      parseDOM: [
        { tag: "strong" },
        { tag: "b" },
        { style: "font-weight", getAttrs: (v) => /^(bold(er)?|[5-9]\d{2,})$/.test(String(v)) && null },
      ],
      toDOM: () => ["strong", 0],
    };
  }

  override inputRules(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? [markInputRule("**", type)] : [];
  }

  override keys(options: ExtensionTypeOptions): Record<string, Command> {
    const type = options.type as MarkType | undefined;
    return type ? { "Mod-b": toggleMark(type), "Mod-B": toggleMark(type) } : {};
  }

  override commands(options: ExtensionTypeOptions) {
    const type = options.type as MarkType | undefined;
    return type ? () => toggleMark(type) : undefined;
  }

  override toMarkdown() {
    return { open: "**", close: "**", mixable: true, expelEnclosingWhitespace: true };
  }

  override parseMarkdown() {
    return { mark: "strong" } as const;
  }
}
