import type { ComponentProps } from "./types";
import Node from "./Node";

/**
 * A Node that renders a React component as its NodeView instead of plain DOM.
 * Subclasses implement `component`, receiving props with access to the editor view,
 * the node, and an `updateAttrs` helper to write structured data back.
 */
export default abstract class ReactNode extends Node {
  abstract component: (props: ComponentProps) => React.ReactElement;
}
