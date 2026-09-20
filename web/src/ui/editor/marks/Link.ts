import type { Command } from "prosemirror-state";
import type { MarkSpec, MarkType } from "prosemirror-model";
import type Token from "markdown-it/lib/token.mjs";
import Mark from "../core/Mark";
import type { CommandFactory, ExtensionTypeOptions, MarkSerializerSpec } from "../core/types";

export default class Link extends Mark {
  override get name(): string {
    return "link";
  }

  override get schema(): MarkSpec {
    return {
      attrs: { href: {}, title: { default: null } },
      inclusive: false,
      parseDOM: [
        {
          tag: "a[href]",
          getAttrs: (dom) => ({
            href: (dom as HTMLElement).getAttribute("href") || "",
            title: (dom as HTMLElement).getAttribute("title"),
          }),
        },
      ],
      toDOM(node) {
        return ["a", { href: node.attrs.href, title: node.attrs.title || undefined, rel: "noopener noreferrer" }, 0];
      },
    };
  }

  override keys(): Record<string, Command> {
    return {};
  }

  /** Apply a link mark to the selection (toolbar / "create link" menu). */
  override commands(options: ExtensionTypeOptions): CommandFactory | undefined {
    const type = options.type as MarkType | undefined;
    if (!type) return undefined;
    return (attrs) => (state, dispatch) => {
      const { empty, ranges } = state.selection;
      if (empty) return false;
      const href = (attrs as { href?: string } | undefined)?.href ?? "";
      let tr = state.tr;
      for (const range of ranges) {
        tr = tr.addMark(range.$from.pos, range.$to.pos, type.create({ href }));
      }
      dispatch?.(tr.scrollIntoView());
      return true;
    };
  }

  override toMarkdown(): MarkSerializerSpec {
    const closeFn: MarkSerializerSpec["close"] = (_state, mark) => {
      const href = String(mark.attrs.href).replace(/[\(\)"]/g, "\\$&");
      const title = mark.attrs.title
        ? ` "${String(mark.attrs.title).replace(/"/g, '\\"')}"`
        : "";
      return "](" + href + title + ")";
    };
    return {
      open: "[",
      close: closeFn,
      mixable: true,
    };
  }

  override parseMarkdown() {
    return {
      mark: "link",
      getAttrs: (tok: Token) => ({
        href: tok.attrGet("href") || "",
        title: tok.attrGet("title"),
      }),
    } as const;
  }
}
