import type { MarkdownSerializerState } from "prosemirror-markdown";
import type Token from "markdown-it/lib/token.mjs";
import type { Node as ProsemirrorNode, NodeSpec } from "prosemirror-model";
import { Plugin, TextSelection, type Command, type Transaction, type EditorState } from "prosemirror-state";
import Node from "../core/Node";

export default class CheckboxItem extends Node {
  override get name(): string {
    return "checkbox_item";
  }

  override get schema(): NodeSpec {
    return {
      content: "block+",
      defining: true,
      attrs: { checked: { default: false } },
      parseDOM: [{ tag: "li.checkbox-item" }],
      toDOM: (node) => [
        "li",
        {
          class: "checkbox-item" + (node.attrs.checked ? " checked" : ""),
          "data-checked": String(node.attrs.checked),
        },
        ["span", { class: "checkbox-toggle", contentEditable: "false" }],
        ["div", { class: "checkbox-content" }, 0],
      ],
    };
  }

  override get plugins(): Plugin[] {
    return [
      new Plugin({
        props: {
          handleClickOn: (view, _pos, node, nodePos, event) => {
            if (node.type.name !== "checkbox_item") return false;
            const target = event.target as HTMLElement;
            if (target.classList.contains("checkbox-toggle")) {
              const tr = view.state.tr.setNodeMarkup(nodePos, undefined, { checked: !node.attrs.checked });
              view.dispatch(tr);
              return true;
            }
            return false;
          },
        },
      }),
    ];
  }

  override keys(): Record<string, Command> {
    return {
      "Mod-Enter": toggleCheckedAt,
      Enter: splitCheckboxItem,
    };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    state.renderContent(node);
  }

  override parseMarkdown() {
    return {
      block: "checkbox_item",
      getAttrs: (tok: Token) => ({ checked: tok.attrGet("checked") === "true" }),
    } as const;
  }
}

function toggleCheckedAt(state: EditorState, dispatch?: (tr: Transaction) => void): boolean {
  const { $from } = state.selection;
  const item = $from.node(-1);
  if (!item || item.type.name !== "checkbox_item") return false;
  const pos = $from.before(-1);
  dispatch?.(state.tr.setNodeMarkup(pos, undefined, { checked: !item.attrs.checked }));
  return true;
}

function splitCheckboxItem(state: EditorState, dispatch?: (tr: Transaction) => void): boolean {
  const { $from } = state.selection;
  const item = $from.node(-1);
  if (!item || item.type.name !== "checkbox_item") return false;

  if (!item.textContent.trim()) {
    return exitList(state, dispatch);
  }

  const schema = state.schema;
  const itemType = schema.nodes["checkbox_item"];
  const paraType = schema.nodes["paragraph"];
  if (!itemType || !paraType) return false;

  const tr = state.tr.split($from.pos, 2, [
    { type: itemType, attrs: { checked: false } },
    { type: paraType },
  ]);
  dispatch?.(tr.scrollIntoView());
  return true;
}

function exitList(state: EditorState, dispatch?: (tr: Transaction) => void): boolean {
  const { $from } = state.selection;
  const itemPos = $from.before(-1);
  const itemNode = $from.node(-1);
  const listPos = $from.before(-2);
  const listNode = $from.node(-2);
  const paraType = state.schema.nodes["paragraph"];
  if (!paraType) return false;

  const tr = state.tr;
  tr.delete(itemPos, itemPos + itemNode.nodeSize);

  const updatedList = tr.doc.nodeAt(listPos);
  const listEmpty = !updatedList || updatedList.childCount === 0;
  if (listEmpty) {
    tr.delete(listPos, listPos + (updatedList?.nodeSize ?? 0));
  }

  const paraPos = listEmpty ? listPos : listPos + listNode.nodeSize - itemNode.nodeSize;
  tr.insert(paraPos, paraType.create());
  tr.setSelection(TextSelection.near(tr.doc.resolve(paraPos + 1)));
  dispatch?.(tr.scrollIntoView());
  return true;
}
