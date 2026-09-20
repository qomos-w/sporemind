import type { NodeSpec } from "prosemirror-model";
import Node from "../core/Node";

export default class Doc extends Node {
  override get name(): string {
    return "doc";
  }

  override get schema(): NodeSpec {
    return { content: "block+" };
  }
}
