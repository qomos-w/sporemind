import { Plugin, PluginKey } from "prosemirror-state";
import type { EditorView } from "prosemirror-view";
import type { CommandFactory } from "../core/types";

export const formatBarKey = new PluginKey<FormatBarState>("formatBar");

export interface FormatBarState {
  open: boolean;
  top: number;
  left: number;
}

type MarkCheck = "strong" | "em" | "code" | "s";

export function formattingToolbarPlugin(commands: Record<string, CommandFactory>): Plugin {
  return new Plugin<FormatBarState>({
    key: formatBarKey,
    state: {
      init: () => ({ open: false, top: 0, left: 0 }),
      apply(tr, value) {
        const meta = tr.getMeta(formatBarKey);
        return meta ?? value;
      },
    },
    view() {
      return {
        update: (v) => renderBar(v, commands),
        destroy: () => destroyBar(),
      };
    },
    props: {
      handleDOMEvents: {
        mouseup: (view) => updateFromSelection(view),
        keyup: (view) => updateFromSelection(view),
      },
    },
  });
}

function updateFromSelection(view: EditorView) {
  const { state } = view;
  const { empty, from, to } = state.selection;
  const isTextSelection = state.selection.constructor.name === "TextSelection";
  if (empty || !isTextSelection) {
    hide(view);
    return;
  }
  const sameBlock = state.doc.resolve(from).sameParent(state.doc.resolve(to));
  if (!sameBlock) {
    hide(view);
    return;
  }

  const coords = view.coordsAtPos(from);
  view.dispatch(view.state.tr.setMeta(formatBarKey, {
    open: true, top: coords.top - 44, left: coords.left,
  }));
}

function hide(view: EditorView) {
  const s = formatBarKey.getState(view.state);
  if (s?.open) {
    view.dispatch(view.state.tr.setMeta(formatBarKey, { open: false, top: 0, left: 0 }));
  }
}

// --- imperative DOM rendering ---

let barEl: HTMLDivElement | null = null;

function destroyBar() {
  if (barEl) {
    barEl.remove();
    barEl = null;
  }
}

const BUTTONS: { label: string; name: string; title: string; attrs?: Record<string, unknown> }[] = [
  { label: "B", name: "strong", title: "Bold (Ctrl+B)" },
  { label: "I", name: "em", title: "Italic (Ctrl+I)" },
  { label: "S", name: "s", title: "Strikethrough" },
  { label: "</>", name: "code", title: "Inline code" },
  { label: "H1", name: "heading", title: "Heading 1", attrs: { level: 1 } },
  { label: "H2", name: "heading", title: "Heading 2", attrs: { level: 2 } },
  { label: "H3", name: "heading", title: "Heading 3", attrs: { level: 3 } },
];

function renderBar(view: EditorView, commands: Record<string, CommandFactory>) {
  const state = formatBarKey.getState(view.state);
  if (!state) return;

  if (!state.open) {
    destroyBar();
    return;
  }

  if (!barEl) {
    barEl = document.createElement("div");
    barEl.className = "rce-format-bar";
    document.body.appendChild(barEl);
  }

  barEl.style.top = state.top + "px";
  barEl.style.left = state.left + "px";
  barEl.innerHTML = "";

  const { selection } = view.state;
  const activeMarks = new Set<MarkCheck>();
  selection.$from.marks().forEach((m) => activeMarks.add(m.type.name as MarkCheck));
  const activeNode = selection.$from.parent.type.name;
  const activeLevel = selection.$from.parent.attrs.level;

  let hitHeading = false;
  for (const b of BUTTONS) {
    // Insert separator before the first heading button.
    if (b.name === "heading" && !hitHeading) {
      hitHeading = true;
      const sep = document.createElement("span");
      sep.className = "rce-format-sep";
      barEl.appendChild(sep);
    }

    const btn = document.createElement("button");
    btn.className = "rce-format-btn";
    btn.textContent = b.label;
    btn.title = b.title;

    if (b.name === "strong" && activeMarks.has("strong")) btn.classList.add("active");
    if (b.name === "em" && activeMarks.has("em")) btn.classList.add("active");
    if (b.name === "s" && activeMarks.has("s")) btn.classList.add("active");
    if (b.name === "code" && activeMarks.has("code")) btn.classList.add("active");
    if (b.name === "heading" && activeNode === "heading" && activeLevel === b.attrs?.level) btn.classList.add("active");

    btn.addEventListener("mousedown", (e) => {
      e.preventDefault();
      const fn = commands[b.name];
      if (fn) fn(b.attrs ?? {})(view.state, view.dispatch.bind(view));
      view.focus();
    });
    barEl.appendChild(btn);
  }
}
