import { describe, expect, it } from "vitest";
import { EditorState } from "prosemirror-state";
import { EditorView } from "prosemirror-view";
import { Schema } from "prosemirror-model";
import { history } from "prosemirror-history";
import { keymap } from "prosemirror-keymap";
import { baseKeymap } from "prosemirror-commands";
import { inputRules } from "prosemirror-inputrules";
import ExtensionManager from "./core/ExtensionManager";
import { basicExtensions } from "./presets";
import { blockMenuPlugin, blockMenuKey } from "./extensions/BlockMenu";

const em = new ExtensionManager(basicExtensions);
const schema = new Schema({ nodes: em.nodes, marks: em.marks });
const commands = em.commands(schema);

function makeView() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  return new EditorView(host, {
    state: EditorState.create({
      doc: schema.topNodeType.create(schema.nodes.paragraph!.create()),
      plugins: [
        history(),
        keymap(baseKeymap),
        ...em.keymaps(schema),
        inputRules({ rules: em.inputRules(schema) }),
        blockMenuPlugin(commands, []),
      ],
    }),
  });
}

function typeText(view: EditorView, text: string) {
  for (const ch of text) {
    view.someProp("handleTextInput", (f) =>
      f(view, view.state.selection.from, view.state.selection.to, ch, () => view.state.tr.insertText(ch)),
    ) || view.dispatch(view.state.tr.insertText(ch));
  }
}

function pressKey(view: EditorView, key: string) {
  view.someProp("handleKeyDown", (f) => f(view, { key } as KeyboardEvent));
}

describe("slash menu cleanup", () => {
  it("typing '/' opens menu WITHOUT inserting '/' into doc", () => {
    const view = makeView();
    typeText(view, "/");
    console.log("After '/':", view.state.doc.toString());
    console.log("Menu state:", JSON.stringify(blockMenuKey.getState(view.state)));
    expect(blockMenuKey.getState(view.state)!.open).toBe(true);
    expect(view.state.doc.textContent).toBe("");
    view.destroy();
  });

  it("Escape closes menu, doc stays empty", () => {
    const view = makeView();
    typeText(view, "/");
    pressKey(view, "Escape");
    console.log("After Escape:", view.state.doc.toString());
    expect(blockMenuKey.getState(view.state)!.open).toBe(false);
    expect(view.state.doc.textContent).toBe("");
    view.destroy();
  });

  it("Backspace on empty query closes menu, doc stays empty", () => {
    const view = makeView();
    typeText(view, "/");
    pressKey(view, "Backspace");
    console.log("After Backspace:", view.state.doc.toString());
    expect(blockMenuKey.getState(view.state)!.open).toBe(false);
    expect(view.state.doc.textContent).toBe("");
    view.destroy();
  });

  it("Enter on heading transforms block, no leftover slash", () => {
    const view = makeView();
    typeText(view, "/");
    pressKey(view, "h");
    pressKey(view, "Enter");
    console.log("After Enter:", view.state.doc.toString());
    // No leftover slash in the document
    expect(view.state.doc.textContent).not.toContain("/");
    view.destroy();
  });

  it("typing regular text after dismissing menu works", () => {
    const view = makeView();
    typeText(view, "/");
    pressKey(view, "Escape");
    typeText(view, "hello");
    console.log("After typing 'hello':", view.state.doc.toString());
    expect(view.state.doc.textContent).toBe("hello");
    view.destroy();
  });
});
