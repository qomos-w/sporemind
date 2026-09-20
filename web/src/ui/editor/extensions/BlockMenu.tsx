import { Plugin, PluginKey } from "prosemirror-state";
import type { CommandFactory } from "../core/types";
import type { EditorView } from "prosemirror-view";

export const blockMenuKey = new PluginKey<BlockMenuState>("blockMenu");

export interface BlockMenuState {
  open: boolean;
  query: string;
  top: number;
  left: number;
}

interface MenuDef {
  name: string;
  label: string;
  keywords?: string;
}

const MENU: MenuDef[] = [
  { name: "paragraph", label: "Text", keywords: "paragraph text" },
  { name: "heading", label: "Heading", keywords: "heading title h1 h2 h3" },
  { name: "bullet_list", label: "Bullet list", keywords: "bullet list ul unordered" },
  { name: "ordered_list", label: "Numbered list", keywords: "numbered ordered list ol" },
  { name: "checkbox_list", label: "Task list", keywords: "task checkbox todo list" },
  { name: "table", label: "Table", keywords: "table grid" },
  { name: "blockquote", label: "Quote", keywords: "quote blockquote" },
  { name: "code_block", label: "Code block", keywords: "code fence" },
  { name: "horizontal_rule", label: "Divider", keywords: "hr rule divider" },
];

export function blockMenuPlugin(
  commands: Record<string, CommandFactory>,
  extraMenu: MenuDef[] = [],
): Plugin {
  const allMenu = [...MENU, ...extraMenu];
  return new Plugin<BlockMenuState>({
    key: blockMenuKey,
    state: {
      init: () => ({ open: false, query: "", top: 0, left: 0 }),
      apply(tr, value) {
        const meta = tr.getMeta(blockMenuKey);
        return meta ?? value;
      },
    },
    view() {
      return {
        update: (v) => renderMenu(v, commands, allMenu),
        destroy: () => destroyMenu(),
      };
    },
    props: {
      handleTextInput(view, from, _to, text) {
        if (text !== "/") return false;
        const { $from } = view.state.selection;
        const textBefore = $from.parent.textBetween(0, $from.parentOffset, "\n");
        if (textBefore !== "") return false;

        const coords = view.coordsAtPos(from);
        view.dispatch(view.state.tr.setMeta(blockMenuKey, {
          open: true, query: "", top: coords.top + 20, left: coords.left,
        }));
        return true;
      },
      handleKeyDown(view, event) {
        const state = blockMenuKey.getState(view.state);
        if (!state?.open) return false;
        const key = event.key;
        if (key === "Escape") {
          view.dispatch(view.state.tr.setMeta(blockMenuKey, closeState()));
          return true;
        }
        if (key === "Backspace" && state.query === "") {
          view.dispatch(view.state.tr.setMeta(blockMenuKey, closeState()));
          return true;
        }
        if (key === "Backspace") {
          view.dispatch(view.state.tr.setMeta(blockMenuKey, { ...state, query: state.query.slice(0, -1) }));
          return true;
        }
        if (key === "Enter") {
          const match = filterMenu(state.query, allMenu)[0];
          const fn = match && commands[match.name];
          if (fn) fn({})(view.state, view.dispatch.bind(view));
          view.dispatch(view.state.tr.setMeta(blockMenuKey, closeState()));
          return true;
        }
        if (key.length === 1 && /[a-z0-9 ]/i.test(key)) {
          view.dispatch(view.state.tr.setMeta(blockMenuKey, { ...state, query: state.query + key }));
          return true;
        }
        return false;
      },
    },
  });
}

function closeState(): BlockMenuState {
  return { open: false, query: "", top: 0, left: 0 };
}

function filterMenu(query: string, items: MenuDef[]): MenuDef[] {
  const q = query.toLowerCase().trim();
  if (!q) return items;
  return items.filter(
    (m) => m.label.toLowerCase().includes(q) || (m.keywords ?? "").toLowerCase().includes(q),
  );
}

// --- imperative DOM rendering (no React portal needed) ---

let menuEl: HTMLDivElement | null = null;

function destroyMenu() {
  if (menuEl) {
    menuEl.remove();
    menuEl = null;
  }
}

function renderMenu(view: EditorView, commands: Record<string, CommandFactory>, allMenu: MenuDef[]) {
  const state = blockMenuKey.getState(view.state);
  if (!state) return;

  if (!state.open) {
    destroyMenu();
    return;
  }

  if (!menuEl) {
    menuEl = document.createElement("div");
    menuEl.className = "rce-block-menu";
    document.body.appendChild(menuEl);
  }

  const items = filterMenu(state.query, allMenu).slice(0, 8);
  menuEl.style.top = state.top + "px";
  menuEl.style.left = state.left + "px";
  menuEl.innerHTML = "";

  if (items.length === 0) {
    const empty = document.createElement("div");
    empty.className = "rce-block-menu-empty";
    empty.textContent = "No matches";
    menuEl.appendChild(empty);
    return;
  }

  for (const m of items) {
    const btn = document.createElement("button");
    btn.className = "rce-block-menu-item";
    btn.textContent = m.label;
    btn.addEventListener("mousedown", (e) => {
      e.preventDefault();
      const fn = commands[m.name];
      if (fn) fn({})(view.state, view.dispatch.bind(view));
      view.dispatch(view.state.tr.setMeta(blockMenuKey, closeState()));
      view.focus();
    });
    menuEl.appendChild(btn);
  }
}
