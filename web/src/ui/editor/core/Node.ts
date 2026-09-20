import type { ParseSpec, MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import Extension from "./Extension";

export default abstract class Node<TOptions extends object = object> extends Extension<TOptions> {
  override get type(): "node" {
    return "node";
  }

  get schema(): NodeSpec {
    return {};
  }

  /** Override when the markdown-it token name differs from `name`. */
  get markdownToken(): string {
    return "";
  }

  /**
   * Serialize this node to markdown. Receives the serializer state, which exposes
   * `renderInline`, `renderContent`, `write`, `text`, `closeBlock`, `ensureNewLine`.
   */
  toMarkdown(_state: MarkdownSerializerState, _node: ProsemirrorNode): void {
    // Default no-op; nodes that need to round-trip override this.
  }

  /** How this extension's markdown-it token maps into the document tree. */
  parseMarkdown(): ParseSpec | void {
    return undefined;
  }
}
