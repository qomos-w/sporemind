import type { Command } from "prosemirror-state";
import type { Mark, Node as ProsemirrorNode } from "prosemirror-model";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { EditorView } from "prosemirror-view";

/** A factory producing a configured ProseMirror command (used by toolbar/menu actions). */
export type CommandFactory = (attrs?: Record<string, unknown>) => Command;

/** Props handed to a ReactNode's `component`, giving it access to the editor + node data. */
export interface ComponentProps {
  view: EditorView;
  node: ProsemirrorNode;
  getPos: () => number | undefined;
  isSelected: boolean;
  isEditable: boolean;
  /** Patch the node's attrs via a node-markup transaction. */
  updateAttrs: (attrs: Record<string, unknown>) => void;
  /** Callbacks passed from the editor host (e.g. onWikiWordClick). */
  host?: Record<string, unknown>;
}

/** Structured shape returned by a Mark's `toMarkdown`. Mirrors prosemirror-markdown internals. */
export interface MarkSerializerSpec {
  open: string | ((state: MarkdownSerializerState, mark: Mark, parent: ProsemirrorNode, index: number) => string);
  close: string | ((state: MarkdownSerializerState, mark: Mark, parent: ProsemirrorNode, index: number) => string);
  mixable?: boolean;
  expelEnclosingWhitespace?: boolean;
  escape?: boolean;
}

/** Options passed to extension `keys` / `inputRules` / `commands`. */
export interface ExtensionTypeOptions {
  type?: import("prosemirror-model").NodeType | import("prosemirror-model").MarkType;
  schema: import("prosemirror-model").Schema;
}
