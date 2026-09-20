import MarkdownIt from "markdown-it";
import {
  MarkdownParser,
  MarkdownSerializer,
  type ParseSpec,
  type MarkdownSerializerState,
} from "prosemirror-markdown";
import { keymap } from "prosemirror-keymap";
import type { InputRule } from "prosemirror-inputrules";
import type { Plugin } from "prosemirror-state";
import type { EditorView } from "prosemirror-view";
import type { MarkSpec, NodeSpec, Node as ProsemirrorNode, Schema } from "prosemirror-model";
import type Extension from "./Extension";
import type Node from "./Node";
import type Mark from "./Mark";
import { ComponentView } from "./ComponentView";
import type { CommandFactory, ComponentProps, ExtensionTypeOptions } from "./types";

export type ExtensionClass = new (options?: Record<string, unknown>) => Extension;

/**
 * markdown-it token handling for content the schema does not yet support (until later
 * stages add the matching nodes/marks). Wrap tokens (base names that produce `_open`/
 * `_close`) keep their inner text; atomic tokens are dropped.
 */
const IGNORED_WRAP_TOKENS: readonly string[] = [
  "blockquote", "bullet_list", "ordered_list", "list_item",
  "em", "strong", "s", "del", "mark", "link",
];

const IGNORED_ATOMIC_TOKENS: readonly string[] = [
  "hr", "code_inline", "image", "autolink", "html_inline", "html_block",
];

export default class ExtensionManager {
  extensions: Extension[] = [];

  constructor(classes: ExtensionClass[], options: Record<string, unknown> = {}) {
    this.extensions = classes.map((Cls) => new Cls(options));
  }

  get nodes(): Record<string, NodeSpec> {
    return Object.fromEntries(
      this.extensions
        .filter((e) => e.type === "node")
        .map((n) => [n.name, (n as Node).schema]),
    );
  }

  get marks(): Record<string, MarkSpec> {
    return Object.fromEntries(
      this.extensions
        .filter((e) => e.type === "mark")
        .map((m) => [m.name, (m as Mark).schema]),
    );
  }

  serializer(): MarkdownSerializer {
    const nodes = Object.fromEntries(
      this.extensions
        .filter((e) => e.type === "node")
        .map((n) => [
          n.name,
          (state: MarkdownSerializerState, node: import("prosemirror-model").Node) =>
            (n as Node).toMarkdown(state, node),
        ]),
    );
    const marks = Object.fromEntries(
      this.extensions
        .filter((e) => e.type === "mark")
        .map((m) => [m.name, (m as Mark).toMarkdown()]),
    );
    return new MarkdownSerializer(nodes, marks, { hardBreakNodeName: "br" });
  }

  parser(schema: Schema): MarkdownParser {
    const tokenizer = new MarkdownIt("commonmark", { html: false });

    // Apply custom markdown-it rules (e.g. strikethrough, future wikiword) from extensions.
    for (const ext of this.extensions) {
      if (ext.rulePlugins.length) {
        for (const rule of ext.rulePlugins) tokenizer.use(rule);
      }
    }

    const tokens: Record<string, ParseSpec> = {};

    for (const ext of this.extensions) {
      if (ext.type !== "node" && ext.type !== "mark") continue;
      const nodeOrMark = ext as Node | Mark;
      const spec = nodeOrMark.parseMarkdown();
      if (spec) {
        tokens[nodeOrMark.markdownToken || nodeOrMark.name] = spec;
      }
    }

    const supported = new Set(Object.keys(tokens));
    for (const token of IGNORED_WRAP_TOKENS) {
      if (!supported.has(token)) tokens[token] = { ignore: true };
    }
    for (const token of IGNORED_ATOMIC_TOKENS) {
      if (!supported.has(token)) tokens[token] = { ignore: true, noCloseToken: true };
    }
    // Fenced/code blocks and headings preserve their text as paragraphs until dedicated
    // nodes are registered (they carry inline content directly, so ignoring would leak).
    if (!supported.has("heading")) tokens.heading = { block: "paragraph" };
    if (!supported.has("fence")) tokens.fence = { block: "paragraph", noCloseToken: true };
    if (!supported.has("code_block")) tokens.code_block = { block: "paragraph", noCloseToken: true };

    return new MarkdownParser(schema, tokenizer, tokens);
  }

  get plugins(): Plugin[] {
    return this.extensions.flatMap((e) => e.plugins);
  }

  keymaps(schema: Schema): Plugin[] {
    return this.extensions
      .map((ext) => ext.keys(this.typeOptions(ext, schema)))
      .filter((bindings) => Object.keys(bindings).length > 0)
      .map((bindings) => keymap(bindings));
  }

  inputRules(schema: Schema): InputRule[] {
    return this.extensions.flatMap((ext) => ext.inputRules(this.typeOptions(ext, schema)));
  }

  /**
   * Build ProseMirror NodeView constructors for every extension that declares a
   * `component` property (ReactNode). Returns a { nodeName: NodeViewConstructor } map.
   */
  nodeViews(host?: Record<string, unknown>): Record<
    string,
    (node: ProsemirrorNode, view: EditorView, getPos: () => number | undefined) => ComponentView
  > {
    const map: Record<
      string,
      (node: ProsemirrorNode, view: EditorView, getPos: () => number | undefined) => ComponentView
    > = {};
    for (const ext of this.extensions) {
      const reactNode = ext as unknown as { component?: (props: ComponentProps) => React.ReactElement };
      if (!reactNode.component) continue;
      const name = ext.name;
      map[name] = (node, view, getPos) => {
        const spec = (ext as unknown as Node).schema;
        const inline = spec.inline ?? false;
        const updateAttrs = (attrs: Record<string, unknown>) => {
          const pos = getPos();
          if (pos === undefined) return;
          view.dispatch(view.state.tr.setNodeMarkup(pos, undefined, { ...node.attrs, ...attrs }));
        };
        return new ComponentView(
          reactNode.component!,
          {
            view,
            node,
            getPos,
            isSelected: false,
            isEditable: view.editable,
            updateAttrs,
            host,
          },
          inline,
        );
      };
    }
    return map;
  }

  /** Flatten every extension's commands into a single name → CommandFactory map. */
  commands(schema: Schema): Record<string, CommandFactory> {
    const all: Record<string, CommandFactory> = {};
    for (const ext of this.extensions) {
      const value = ext.commands(this.typeOptions(ext, schema));
      if (!value) continue;
      if (typeof value === "function") {
        all[ext.name] = value;
      } else {
        Object.assign(all, value);
      }
    }
    return all;
  }

  private typeOptions(ext: Extension, schema: Schema): ExtensionTypeOptions {
    if (ext.type === "node") return { type: schema.nodes[ext.name], schema };
    if (ext.type === "mark") return { type: schema.marks[ext.name], schema };
    return { schema };
  }
}
