import { Plugin, PluginKey } from "prosemirror-state";
import type { EditorView } from "prosemirror-view";
import { findTable } from "prosemirror-tables";
import type { CommandFactory } from "../core/types";

export const tableToolbarKey = new PluginKey<TableToolbarState>("tableToolbar");

export interface TableToolbarState {
  open: boolean;
  top: number;
  left: number;
}

interface ButtonDef {
  name: string;
  label: string;
  title: string;
}

const BUTTONS: ButtonDef[] = [
  { name: "add_row_before", label: "+R↑", title: "Add row above" },
  { name: "add_row_after", label: "+R↓", title: "Add row below" },
  { name: "add_column_before", label: "+C←", title: "Add column left" },
  { name: "add_column_after", label: "+C→", title: "Add column right" },
  { name: "delete_row", label: "-R", title: "Delete row" },
  { name: "delete_column", label: "-C", title: "Delete column" },
  { name: "delete_table", label: "×T", title: "Delete table" },
];

export function tableToolbarPlugin(commands: Record<string, CommandFactory>): Plugin {
  return new Plugin<TableToolbarState>({
    key: tableToolbarKey,
    state: {
      init: () => ({ open: false, top: 0, left: 0 }),
      apply(tr, value) {
        const meta = tr.getMeta(tableToolbarKey);
        return meta ?? value;
      },
    },
    view() {
      return {
        update: (v) => renderToolbar(v, commands),
        destroy: () => destroyToolbar(),
      };
    },
    props: {
      handleDOMEvents: {
        keyup: (view) => updateFromSelection(view),
        mouseup: (view) => updateFromSelection(view),
      },
    },
  });
}

function updateFromSelection(view: EditorView) {
  const { state } = view;
  const { $from } = state.selection;
  const table = findTable($from);
  if (!table) {
    hide(view);
    return;
  }

  const coords = view.coordsAtPos($from.pos);
  view.dispatch(
    view.state.tr.setMeta(tableToolbarKey, {
      open: true,
      top: coords.top - 36,
      left: coords.left,
    }),
  );
}

function hide(view: EditorView) {
  const s = tableToolbarKey.getState(view.state);
  if (s?.open) {
    view.dispatch(view.state.tr.setMeta(tableToolbarKey, { open: false, top: 0, left: 0 }));
  }
}

let toolbarEl: HTMLDivElement | null = null;

function destroyToolbar() {
  if (toolbarEl) {
    toolbarEl.remove();
    toolbarEl = null;
  }
}

function renderToolbar(view: EditorView, commands: Record<string, CommandFactory>) {
  const state = tableToolbarKey.getState(view.state);
  if (!state) return;

  if (!state.open) {
    destroyToolbar();
    return;
  }

  if (!toolbarEl) {
    toolbarEl = document.createElement("div");
    toolbarEl.className = "rce-table-toolbar";
    document.body.appendChild(toolbarEl);
  }

  toolbarEl.style.top = state.top + "px";
  toolbarEl.style.left = state.left + "px";
  toolbarEl.innerHTML = "";

  for (const b of BUTTONS) {
    const btn = document.createElement("button");
    btn.className = "rce-table-toolbar-btn";
    btn.textContent = b.label;
    btn.title = b.title;
    btn.addEventListener("mousedown", (e) => {
      e.preventDefault();
      const fn = commands[b.name];
      if (fn) fn({})(view.state, view.dispatch.bind(view));
      view.focus();
    });
    toolbarEl.appendChild(btn);
  }
}
