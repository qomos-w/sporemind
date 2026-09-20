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
import { plusButtonPlugin } from "./extensions/PlusButton";

const em = new ExtensionManager(basicExtensions);
const schema = new Schema({ nodes: em.nodes, marks: em.marks });
const commands = em.commands(schema);

describe("plus button opens block menu", () => {
  it("dispatching blockMenu meta opens the menu", () => {
    const host = document.createElement("div");
    document.body.appendChild(host);

    const plugins = [
      history(),
      keymap(baseKeymap),
      inputRules({ rules: em.inputRules(schema) }),
      plusButtonPlugin(commands, []),
      blockMenuPlugin(commands, []),
    ];

    const view = new EditorView(host, {
      state: EditorState.create({
        doc: schema.topNodeType.create(schema.nodes.paragraph!.create()),
        plugins,
      }),
    });

    // Simulate what PlusButton does: dispatch meta to open menu
    view.dispatch(
      view.state.tr.setMeta(blockMenuKey, {
        open: true,
        query: "",
        top: 100,
        left: 50,
      }),
    );

    const state = blockMenuKey.getState(view.state);
    console.log("After dispatch — blockMenu state:", JSON.stringify(state));

    // Check if the portal has content
    const portal = document.body.querySelector(".rce-block-menu");
    console.log("Portal element found:", !!portal);
    if (portal) {
      console.log("Portal HTML:", portal.outerHTML.slice(0, 200));
      console.log("Items:", portal.querySelectorAll(".rce-block-menu-item").length);
    }

    expect(state?.open).toBe(true);

    view.destroy();
  });
});
