import { Plugin, PluginKey } from "prosemirror-state";
import { Decoration, DecorationSet, type EditorView } from "prosemirror-view";
import type { CommandFactory } from "../core/types";
import { blockMenuKey } from "./BlockMenu";

export const plusButtonKey = new PluginKey("plusButton");

interface MenuDef {
  name: string;
  label: string;
  keywords?: string;
}

/**
 * A plugin that renders a floating `+` button in the left gutter of the current
 * block. Clicking it opens the block format menu (same as `/`). The button only
 * appears when the editor is focused and the cursor is in an empty top-level
 * paragraph — mirroring Outline's block-menu trigger.
 */
export function plusButtonPlugin(
  _commands: Record<string, CommandFactory>,
  _menuItems: MenuDef[],
): Plugin {
  // Captured by the `view()` lifecycle so decorations can dispatch to it.
  let view: EditorView | null = null;

  return new Plugin({
    key: plusButtonKey,
    view(editorView: EditorView) {
      view = editorView;
      return {};
    },
    props: {
      decorations(state) {
        const { selection } = state;
        if (!selection.empty) return null;

        const $from = selection.$from;
        if ($from.depth !== 1) return null;
        const parent = $from.parent;
        // Show on any empty top-level block (paragraph, heading, blockquote, code block, etc.)
        // so the user can re-pick a block type after deleting all text.
        if (parent.content.size > 0) return null;

        const pos = $from.start(1);

        const button = document.createElement("button");
        button.className = "rce-plus-btn";
        button.setAttribute("aria-label", "Add block");
        button.type = "button";
        button.innerHTML =
          '<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';

        button.addEventListener("mousedown", (e) => {
          e.preventDefault();
          if (!view) return;
          const btnRect = button.getBoundingClientRect();
          view.dispatch(
            view.state.tr.setMeta(blockMenuKey, {
              open: true,
              top: btnRect.bottom + 4,
              left: btnRect.left,
              query: "",
            }),
          );
        });

        const deco = Decoration.widget(pos, button, { key: "plus-btn" });
        return DecorationSet.create(state.doc, [deco]);
      },
    },
  });
}
