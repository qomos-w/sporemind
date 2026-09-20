import { describe, expect, it, afterEach } from "vitest";
import React from "react";
import { Schema } from "prosemirror-model";
import ExtensionManager from "./core/ExtensionManager";
import { basicExtensions } from "./presets";
import { CardNodeRegistry } from "./registry";
import { isMarkdown } from "./extensions/MarkdownPaste";
import ReactNode from "./core/ReactNode";
import type { NodeSpec } from "prosemirror-model";

function build() {
  const em = new ExtensionManager(basicExtensions);
  const schema = new Schema({ nodes: em.nodes, marks: em.marks });
  const serializer = em.serializer();
  const parser = em.parser(schema);
  return { parser, serializer };
}

/** parse → serialize once. */
function serialize(md: string): string {
  const { parser, serializer } = build();
  return serializer.serialize(parser.parse(md));
}

/** parse → serialize → parse → serialize. A stable editor is idempotent here. */
function stabilize(md: string): string {
  const { parser, serializer } = build();
  const first = serializer.serialize(parser.parse(md));
  return serializer.serialize(parser.parse(first));
}

describe("markdown round-trip", () => {
  it("is idempotent for a simple paragraph", () => {
    expect(stabilize("Hello world")).toBe(serialize("Hello world"));
  });

  it("preserves paragraph text", () => {
    expect(serialize("Hello world")).toContain("Hello world");
  });

  it("keeps multiple paragraphs separate", () => {
    const out = serialize("First paragraph.\n\nSecond one.");
    expect(out).toContain("First paragraph.");
    expect(out).toContain("Second one.");
  });

  it("preserves hard breaks (Shift-Enter)", () => {
    const out = serialize("line one\\\nline two");
    // round-trip should keep two lines within the same paragraph
    expect(out).toMatch(/line one\\\nline two/);
  });

  it("does not crash on empty input", () => {
    expect(() => serialize("")).not.toThrow();
  });
});

describe("wikiword round-trip", () => {
  it("parses [[wikiname]] into a wikiword node and back", () => {
    const out = serialize("Link to [[SomePage]] here");
    expect(out).toContain("[[SomePage]]");
  });

  it("round-trips a wikiword inside other formatting", () => {
    const md = "See **bold [[Concept]] bold** here";
    expect(serialize(md)).toContain("[[Concept]]");
  });

  it("is idempotent for wikiwords", () => {
    expect(stabilize("Link [[Page]] here")).toBe(serialize("Link [[Page]] here"));
  });

  it("parses [[display|target]] into a wikiword node and back", () => {
    const out = serialize("Link to [[My Label|TargetPage]] here");
    expect(out).toContain("[[My Label|TargetPage]]");
  });

  it("is idempotent for [[display|target]]", () => {
    expect(stabilize("Link [[My Label|TargetPage]] here")).toBe(serialize("Link [[My Label|TargetPage]] here"));
  });

  it("wikiword inside heading round-trips", () => {
    const out = serialize("# Title with [[WikiLink]]");
    expect(out).toContain("# Title with [[WikiLink]]");
  });

  it("[[display|target]] inside heading round-trips", () => {
    const out = serialize("# See [[My Label|SomeCard]]");
    expect(out).toContain("[[My Label|SomeCard]]");
  });

  it("wikiword inside blockquote round-trips", () => {
    const out = serialize("> Quote with [[WikiLink]] inside");
    expect(out).toContain("[[WikiLink]]");
  });

  it("parses [display](wiki:target) into a wikiword node and serializes as [[...]]", () => {
    const out = serialize("Link to [My Label](wiki:TargetPage) here");
    expect(out).toContain("[[My Label|TargetPage]]");
  });

  it("parses [Page](wiki:Page) into [[Page]]", () => {
    const out = serialize("Link to [Page](wiki:Page) here");
    expect(out).toContain("[[Page]]");
  });

  it("leaves regular markdown links as links, not wikiwords", () => {
    const out = serialize("[link](http://example.com) here");
    expect(out).toContain("[link](http://example.com)");
  });
});

describe("card mention round-trip", () => {
  it("parses @[title](card://id) into a card_mention node and back", () => {
    const out = serialize("Ref @[My Card](card://abc123) done");
    expect(out).toContain("@[My Card](card://abc123)");
  });

  it("is idempotent for card mentions", () => {
    expect(stabilize("Ref @[Card](card://id1) end")).toBe(serialize("Ref @[Card](card://id1) end"));
  });
});

describe("checkbox list round-trip", () => {
  it("parses - [ ] and - [x] into checkbox items and back", () => {
    const md = "- [ ] todo\n- [x] done";
    const out = serialize(md);
    expect(out).toContain("[ ]");
    expect(out).toContain("[x]");
  });

  it("is idempotent for checkbox lists", () => {
    const md = "- [ ] task one\n- [x] task two";
    expect(stabilize(md)).toBe(serialize(md));
  });

  it("preserves checkbox item text", () => {
    const out = serialize("- [ ] buy milk");
    expect(out).toContain("buy milk");
  });
});

describe("tolerance of unsupported structures", () => {
  it("does not throw on headings, lists, quotes, emphasis, links", () => {
    const md = "# Title\n\n- item one\n- item two\n\n> a quote\n\n**bold** and _italic_ and [a link](https://x)\n\n1. first\n2. second";
    expect(() => serialize(md)).not.toThrow();
  });

  it("preserves the text content of degraded structures", () => {
    const out = serialize("# Heading\n\n- list item\n\n> quoted\n\n**bold**");
    expect(out).toContain("Heading");
    expect(out).toContain("list item");
    expect(out).toContain("quoted");
    expect(out).toContain("bold");
  });

  it("preserves fenced code block text as a paragraph", () => {
    const out = serialize("```\nlet x = 1\n```");
    expect(out).toContain("let x = 1");
  });
});

describe("table round-trip", () => {
  it("parses a GFM table and serializes it back", () => {
    const md = "| Name | Age |\n| --- | --- |\n| Alice | 30 |\n| Bob | 25 |";
    const out = serialize(md);
    expect(out).toContain("| Name | Age |");
    expect(out).toContain("| --- | --- |");
    expect(out).toContain("| Alice | 30 |");
    expect(out).toContain("| Bob | 25 |");
  });

  it("is idempotent for tables", () => {
    const md = "| A | B |\n|---|---|\n| 1 | 2 |";
    expect(stabilize(md)).toBe(serialize(md));
  });

  it("serializes table rows without blank lines", () => {
    const md = "| A | B |\n|---|---|\n| 1 | 2 |";
    const out = serialize(md);
    expect(out).not.toContain("| A | B |\n\n|");
    expect(out).toMatch(/^\| A \| B \|\n\| --- \| --- \|\n\| 1 \| 2 \|$/m);
  });

  it("preserves inline marks inside table cells", () => {
    const md = "| **bold** | `code` |\n|---|---|";
    const out = serialize(md);
    expect(out).toContain("**bold**");
    expect(out).toContain("`code`");
  });

  it("handles an empty table cell", () => {
    const md = "| A | B |\n|---|---|\n|   | x |";
    const out = serialize(md);
    expect(out).toContain("| A | B |");
    expect(out).toContain("| x |");
  });

  it("detects table rows as markdown for paste", () => {
    expect(isMarkdown("| a | b |\n|---|---|\n| c | d |")).toBe(true);
  });
});

describe("CardNodeRegistry", () => {
  afterEach(() => CardNodeRegistry.clear());

  class TestNode extends ReactNode {
    override get name() { return "test_node"; }
    override component = () => React.createElement("span", null, "test");
    override get schema(): NodeSpec {
      return { inline: true, group: "inline", atom: true, attrs: { value: { default: "" } } };
    }
  }

  it("registers and returns extensions", () => {
    CardNodeRegistry.register(TestNode);
    const exts = CardNodeRegistry.getExtensions();
    expect(exts).toHaveLength(1);
    expect(new (exts[0] as new () => TestNode)().name).toBe("test_node");
  });

  it("registers menu items", () => {
    CardNodeRegistry.register(TestNode, {
      menuItem: { label: "Test Node", keywords: "test demo" },
    });
    const items = CardNodeRegistry.getMenuItems();
    expect(items).toHaveLength(1);
    expect(items[0]!.label).toBe("Test Node");
  });

  it("unregisters by name", () => {
    CardNodeRegistry.register(TestNode);
    expect(CardNodeRegistry.getExtensions()).toHaveLength(1);
    CardNodeRegistry.unregister("test_node");
    expect(CardNodeRegistry.getExtensions()).toHaveLength(0);
  });

  it("extensions registered via registry work in a schema", () => {
    CardNodeRegistry.register(TestNode);
    const exts = [...basicExtensions, ...CardNodeRegistry.getExtensions()];
    expect(() => new ExtensionManager(exts)).not.toThrow();
  });
});

describe("markdown paste detection", () => {
  it("detects headings as markdown", () => {
    expect(isMarkdown("# Title\n\nSome text")).toBe(true);
  });

  it("detects lists as markdown", () => {
    expect(isMarkdown("- item one\n- item two\n- item three")).toBe(true);
  });

  it("detects bold/italic/links as markdown", () => {
    expect(isMarkdown("This is **bold** and [a link](https://x)")).toBe(true);
  });

  it("detects code fences as markdown", () => {
    expect(isMarkdown("```\ncode\n```")).toBe(true);
  });

  it("does not treat plain text as markdown", () => {
    expect(isMarkdown("Just some regular text")).toBe(false);
  });

  it("does not treat a single word as markdown", () => {
    expect(isMarkdown("hello")).toBe(false);
  });
});
