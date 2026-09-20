import { createRoot, type Root } from "react-dom/client";
import type { Node as ProsemirrorNode } from "prosemirror-model";
import type { Decoration, DecorationSource } from "prosemirror-view";
import type { ComponentProps } from "./types";
import type ReactNode from "./ReactNode";

/**
 * Bridges ProseMirror's imperative NodeView API to a React component.
 * Creates a DOM container, mounts the ReactNode's `component` via createRoot,
 * and re-renders on every ProseMirror update by syncing props.
 */
export class ComponentView {
  dom: HTMLElement;
  private root: Root;
  private component: ReactNode["component"];
  private props: ComponentProps;

  constructor(
    component: ReactNode["component"],
    init: ComponentProps,
    inline: boolean,
  ) {
    this.component = component;
    this.dom = inline ? document.createElement("span") : document.createElement("div");
    this.props = init;
    this.root = createRoot(this.dom);
    this.render();
  }

  private render() {
    this.root.render(this.component(this.props));
  }

  update(node: ProsemirrorNode, _decorations: readonly Decoration[], _innerDecorations: DecorationSource): boolean {
    this.props = { ...this.props, node };
    this.render();
    return true;
  }

  selectNode() {
    this.dom.classList.add("ProseMirror-selectednode");
    this.props = { ...this.props, isSelected: true };
    this.render();
  }

  deselectNode() {
    this.dom.classList.remove("ProseMirror-selectednode");
    this.props = { ...this.props, isSelected: false };
    this.render();
  }

  stopEvent(event: Event): boolean {
    const type = event.type;
    if (/^(drag|drop)/.test(type)) return false;
    return true;
  }

  ignoreMutation(): boolean {
    return true;
  }

  destroy() {
    this.root.unmount();
  }
}
