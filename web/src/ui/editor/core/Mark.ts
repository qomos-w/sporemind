import type { ParseSpec } from "prosemirror-markdown";
import type { MarkSpec } from "prosemirror-model";
import Extension from "./Extension";
import type { MarkSerializerSpec } from "./types";

export default abstract class Mark<TOptions extends object = object> extends Extension<TOptions> {
  override get type(): "mark" {
    return "mark";
  }

  get schema(): MarkSpec {
    return {};
  }

  /** Override when the markdown-it token name differs from `name`. */
  get markdownToken(): string {
    return "";
  }

  /** Inline mark serializer spec: the delimiters wrapping marked text. */
  toMarkdown(): MarkSerializerSpec {
    return { open: "", close: "" };
  }

  parseMarkdown(): ParseSpec | void {
    return undefined;
  }
}
