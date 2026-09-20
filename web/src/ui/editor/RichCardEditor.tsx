import React from "react";
import { EditorState, Plugin } from "prosemirror-state";
import { EditorView, Decoration, DecorationSet } from "prosemirror-view";
import { Schema, type Node as ProsemirrorNode } from "prosemirror-model";
import { history } from "prosemirror-history";
import { dropCursor } from "prosemirror-dropcursor";
import { gapCursor } from "prosemirror-gapcursor";
import { keymap } from "prosemirror-keymap";
import { baseKeymap } from "prosemirror-commands";
import { inputRules } from "prosemirror-inputrules";
import { tableEditing } from "prosemirror-tables";
import { type MarkdownParser, type MarkdownSerializer } from "prosemirror-markdown";
import ExtensionManager, { type ExtensionClass } from "./core/ExtensionManager";
import type { CommandFactory } from "./core/types";
import { blockMenuPlugin } from "./extensions/BlockMenu";
import { formattingToolbarPlugin } from "./extensions/FormattingToolbar";
import { tableToolbarPlugin } from "./extensions/TableToolbar";
import { markdownPastePlugin } from "./extensions/MarkdownPaste";
import { plusButtonPlugin } from "./extensions/PlusButton";
import { basicExtensions } from "./presets";
import { CardNodeRegistry } from "./registry";
import "./RichCardEditor.css";
import "../markdown-content.css";

export interface CursorPosition {
  line: number;
  column: number;
  offset: number;
}

export interface RichCardEditorProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  extensions?: ExtensionClass[];
  onWikiWordClick?: (word: string) => void;
  showCursorPosition?: boolean;
  onCursorPositionChange?: (pos: CursorPosition) => void;
  cursorPositionFormatter?: (pos: CursorPosition) => string;
}

interface RichCardEditorState {
  cursorPosition: CursorPosition;
}

/**
 * A ProseMirror-backed rich text editor whose content is a Markdown string.
 * It is a drop-in replacement for the string-in/string-out BlockEditor.
 */
export class RichCardEditor extends React.PureComponent<RichCardEditorProps, RichCardEditorState> {
  static defaultProps = {
    placeholder: "Write something…",
    extensions: basicExtensions,
  };

  private hostRef = React.createRef<HTMLDivElement>();
  private view!: EditorView;
  private schema!: Schema;
  private serializer!: MarkdownSerializer;
  private parser!: MarkdownParser;
  private commands!: Record<string, CommandFactory>;
  private lastEmitted = "";

  state: RichCardEditorState = { cursorPosition: { line: 1, column: 1, offset: 0 } };

  componentDidMount(): void {
    const registered = CardNodeRegistry.getExtensions();
    const extensions = this.props.extensions ?? [...basicExtensions, ...registered];
    const em = new ExtensionManager(extensions);
    this.schema = new Schema({ nodes: em.nodes, marks: em.marks });
    this.serializer = em.serializer();
    this.parser = em.parser(this.schema);
    this.commands = em.commands(this.schema);

    const extraMenu = CardNodeRegistry.getMenuItems().map((m) => ({
      name: m.name,
      label: m.label,
      keywords: m.keywords,
    }));

    const plugins: Plugin[] = [
      history(),
      dropCursor(),
      gapCursor(),
      keymap(baseKeymap),
      ...em.keymaps(this.schema),
      inputRules({ rules: em.inputRules(this.schema) }),
      tableEditing(),
      ...em.plugins,
      plusButtonPlugin(this.commands, extraMenu),
      blockMenuPlugin(this.commands, extraMenu),
      formattingToolbarPlugin(this.commands),
      tableToolbarPlugin(this.commands),
      markdownPastePlugin(this.parser),
      placeholderPlugin(this.props.placeholder ?? ""),
    ];

    this.view = new EditorView(this.hostRef.current!, {
      state: EditorState.create({ doc: this.parseSafe(this.props.value), plugins }),
      attributes: { class: 'markdown-content' },
      nodeViews: em.nodeViews(
        this.props.onWikiWordClick
          ? { onWikiWordClick: this.props.onWikiWordClick }
          : undefined,
      ),
      dispatchTransaction: (tr) => {
        const next = this.view.state.apply(tr);
        this.view.updateState(next);
        if (tr.docChanged || tr.selectionSet) {
          const pos = this.computeCursorPosition(next);
          if (this.props.showCursorPosition) {
            this.setState({ cursorPosition: pos });
          }
          this.props.onCursorPositionChange?.(pos);
        }
        if (tr.docChanged) {
          const md = this.serializer.serialize(next.doc);
          this.lastEmitted = md;
          this.props.onChange(md);
        }
      },
    });
  }

  componentDidUpdate(prev: RichCardEditorProps): void {
    if (this.props.value !== prev.value && this.props.value !== this.lastEmitted) {
      this.view.updateState(
        EditorState.create({
          doc: this.parseSafe(this.props.value),
          plugins: this.view.state.plugins,
        }),
      );
      const pos = this.computeCursorPosition(this.view.state);
      if (this.props.showCursorPosition) {
        this.setState({ cursorPosition: pos });
      }
      this.props.onCursorPositionChange?.(pos);
    }
  }

  componentWillUnmount(): void {
    this.view?.destroy();
  }

  focus(): void {
    this.view?.focus();
  }

  /** Parse a markdown string, tolerating parse errors by falling back to plain text. */
  private parseSafe(value: string): ProsemirrorNode {
    let parsed: ProsemirrorNode;
    try {
      parsed = this.parser.parse(value);
    } catch {
      return this.schema.topNodeType.create(this.schema.nodes.paragraph!.create());
    }
    if (parsed.content.size === 0) {
      return this.schema.topNodeType.create(this.schema.nodes.paragraph!.create());
    }
    return parsed;
  }

  private isLeafBlock(node: ProsemirrorNode): boolean {
    if (!node.isBlock) return false;
    if (node.childCount === 0) return true;
    return !node.content.firstChild?.isBlock;
  }

  private computeCursorPosition(state: EditorState): CursorPosition {
    const pos = state.selection.head;
    const resolved = state.doc.resolve(pos);
    let blockDepth = 0;
    for (let d = resolved.depth; d > 0; d--) {
      if (this.isLeafBlock(resolved.node(d))) {
        blockDepth = d;
        break;
      }
    }

    let count = 0;
    state.doc.nodesBetween(0, pos, (node, start) => {
      if (this.isLeafBlock(node) && start < pos) {
        count++;
      }
      return true;
    });
    const atBlockStart = pos === resolved.start(blockDepth);
    const line = count + (atBlockStart ? 1 : 0);
    const column = Math.max(1, pos - resolved.start(blockDepth) + 1);
    return { line: Math.max(1, line), column, offset: pos };
  }

  render() {
    const pos = this.state.cursorPosition;
    const positionText = this.props.cursorPositionFormatter
      ? this.props.cursorPositionFormatter(pos)
      : `Ln ${pos.line}, Col ${pos.column}`;
    return (
      <div className="rich-card-editor-host">
        <div ref={this.hostRef} className="rich-card-editor" />
        {this.props.showCursorPosition && (
          <div className="rich-card-editor-status">
            {positionText}
          </div>
        )}
      </div>
    );
  }
}

/** Shows placeholder text on an empty first paragraph via a node decoration. */
function placeholderPlugin(text: string): Plugin {
  return new Plugin({
    props: {
      decorations: (state) => {
        const { doc } = state;
        const first = doc.firstChild;
        if (
          doc.childCount === 1 &&
          first?.type.name === "paragraph" &&
          first.content.size === 0
        ) {
          return DecorationSet.create(doc, [
            Decoration.node(0, first.nodeSize, {
              "data-placeholder": text,
              class: "rce-empty",
            }),
          ]);
        }
        return DecorationSet.empty;
      },
    },
  });
}
