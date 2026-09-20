import { describe, expect, it } from "vitest";
import { EditorState, TextSelection } from "prosemirror-state";
import { EditorView } from "prosemirror-view";
import { Schema } from "prosemirror-model";
import { history, undo, redo } from "prosemirror-history";
import { keymap } from "prosemirror-keymap";
import { baseKeymap } from "prosemirror-commands";
import { inputRules } from "prosemirror-inputrules";
import ExtensionManager from "./core/ExtensionManager";
import { basicExtensions } from "./presets";

const em = new ExtensionManager(basicExtensions);
const schema = new Schema({ nodes: em.nodes, marks: em.marks });
const parser = em.parser(schema);

function emptyView() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  return new EditorView(host, {
    state: EditorState.create({
      doc: schema.topNodeType.create(schema.nodes.paragraph!.create()),
      plugins: [history(), keymap(baseKeymap), inputRules({ rules: em.inputRules(schema) })],
    }),
  });
}

function docView(markdown: string) {
  const host = document.createElement("div");
  document.body.appendChild(host);
  return new EditorView(host, {
    state: EditorState.create({
      doc: parser.parse(markdown)!,
      plugins: [history(), keymap(baseKeymap), inputRules({ rules: em.inputRules(schema) })],
    }),
  });
}

/** Simulate typing character-by-character; insert text when no input rule matches. */
function typeText(view: EditorView, text: string) {
  for (const ch of text) {
    const handled = view.someProp("handleTextInput", (f) =>
      f(view, view.state.selection.from, view.state.selection.to, ch, () => view.state.tr),
    );
    if (!handled) view.dispatch(view.state.tr.insertText(ch));
  }
}

describe("undo behavior with inline component nodes", () => {
  it("typing [[wiki]] creates a wikiword chip", () => {
    const view = emptyView();
    typeText(view, "[[wiki]]");
    expect(view.state.doc.toString()).toContain("wikiword");
    view.destroy();
  });

  it("undo after input rule does not expose raw [[wiki]] text", () => {
    const view = emptyView();
    typeText(view, "[[wiki]]");
    expect(view.state.doc.toString()).toContain("wikiword");

    undo(view.state, view.dispatch.bind(view));

    // Chip is gone and raw markdown syntax is NOT shown.
    expect(view.state.doc.toString()).not.toContain("wikiword");
    expect(view.state.doc.textContent).not.toContain("[[wiki]]");
    view.destroy();
  });

  it("undo of text edit near a chip preserves the chip as a node", () => {
    const view = docView("Before [[Target]] after");
    expect(view.state.doc.toString()).toContain("wikiword");

    // Type extra text at end
    const endPos = view.state.doc.content.size - 1;
    view.dispatch(view.state.tr.setSelection(TextSelection.near(view.state.doc.resolve(endPos))));
    typeText(view, " more");

    // Undo the typing
    undo(view.state, view.dispatch.bind(view));

    // The chip remains a node — never converted to raw text
    expect(view.state.doc.toString()).toContain("wikiword");
    expect(view.state.doc.toString()).not.toContain("[[Target]]");
    view.destroy();
  });

  it("redo restores the chip after undo", () => {
    const view = docView("Hello [[World]] end");
    expect(view.state.doc.toString()).toContain("wikiword");

    // Edit, then undo
    const endPos = view.state.doc.content.size - 1;
    view.dispatch(view.state.tr.setSelection(TextSelection.near(view.state.doc.resolve(endPos))));
    typeText(view, "!");
    undo(view.state, view.dispatch.bind(view));

    // Redo — chip should still be intact
    redo(view.state, view.dispatch.bind(view));
    expect(view.state.doc.toString()).toContain("wikiword");
    view.destroy();
  });
});
