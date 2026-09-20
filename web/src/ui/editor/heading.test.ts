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

const em = new ExtensionManager(basicExtensions);
const schema = new Schema({ nodes: em.nodes, marks: em.marks });

function makeView() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  return new EditorView(host, {
    state: EditorState.create({
      doc: schema.topNodeType.create(schema.nodes.paragraph!.create()),
      plugins: [...em.keymaps(schema), history(), keymap(baseKeymap), inputRules({ rules: em.inputRules(schema) })],
    }),
  });
}

function typeText(view: EditorView, text: string) {
  for (const ch of text) {
    const handled = view.someProp("handleTextInput", (f) =>
      f(view, view.state.selection.from, view.state.selection.to, ch, () => view.state.tr.insertText(ch)),
    );
    if (!handled) view.dispatch(view.state.tr.insertText(ch));
  }
}

describe("heading input rule", () => {
  it("typing '# ' converts paragraph to heading", () => {
    const view = makeView();
    typeText(view, "# ");

    console.log("After '# ':", view.state.doc.toString());
    console.log("Selection:", JSON.stringify({ from: view.state.selection.from, to: view.state.selection.to }));

    expect(view.state.doc.toString()).toContain("heading");
    view.destroy();
  });

  it("typing '#Title' (no space) does NOT convert", () => {
    const view = makeView();
    typeText(view, "#Title");

    console.log("After '#Title':", view.state.doc.toString());
    expect(view.state.doc.toString()).not.toContain("heading");
    expect(view.state.doc.toString()).toContain("#Title");
    view.destroy();
  });

  it("typing '# Title' produces a heading with text 'Title'", () => {
    const view = makeView();
    typeText(view, "# Title");

    console.log("After '# Title':", view.state.doc.toString());
    expect(view.state.doc.toString()).toContain("heading");
    expect(view.state.doc.textContent).toBe("Title");
    view.destroy();
  });

  it("heading conversion does NOT create extra paragraph", () => {
    const view = makeView();
    typeText(view, "# Title");

    const docStr = view.state.doc.toString();
    console.log("Doc structure:", docStr);

    // Should be exactly: doc(heading("Title"))
    // NOT: doc(heading("Title"), paragraph) or doc(paragraph, heading)
    const blockCount = view.state.doc.childCount;
    console.log("Block count:", blockCount);

    expect(blockCount).toBe(1);
    view.destroy();
  });

  it("Backspace on empty heading reverts to paragraph", () => {
    const view = makeView();
    typeText(view, "# Title");
    expect(view.state.doc.toString()).toContain("heading");

    // Delete all text
    view.dispatch(view.state.tr.delete(1, view.state.doc.content.size - 1));
    expect(view.state.doc.toString()).toContain("heading");

    // Backspace at start of empty heading → should become paragraph
    view.someProp("handleKeyDown", (f) => f(view, { key: "Backspace" } as KeyboardEvent));

    console.log("After Backspace:", view.state.doc.toString());
    expect(view.state.doc.toString()).not.toContain("heading");
    expect(view.state.doc.toString()).toContain("paragraph");
    view.destroy();
  });

  it("Backspace on empty code_block reverts to paragraph", () => {
    const view = makeView();
    typeText(view, "```");
    expect(view.state.doc.toString()).toContain("code_block");

    // Simulate Backspace key at start of empty code_block
    view.someProp("handleKeyDown", (f) => f(view, { key: "Backspace" } as KeyboardEvent));

    console.log("After Backspace:", view.state.doc.toString());
    expect(view.state.doc.toString()).not.toContain("code_block");
    expect(view.state.doc.toString()).toContain("paragraph");
    view.destroy();
  });
});
