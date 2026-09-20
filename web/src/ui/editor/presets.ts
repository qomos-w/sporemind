import Doc from "./nodes/Doc";
import Paragraph from "./nodes/Paragraph";
import Text from "./nodes/Text";
import HardBreak from "./nodes/HardBreak";
import Heading from "./nodes/Heading";
import Blockquote from "./nodes/Blockquote";
import HorizontalRule from "./nodes/HorizontalRule";
import CodeBlock from "./nodes/CodeBlock";
import BulletList from "./nodes/BulletList";
import OrderedList from "./nodes/OrderedList";
import ListItem from "./nodes/ListItem";
import Bold from "./marks/Bold";
import Italic from "./marks/Italic";
import Code from "./marks/Code";
import Strikethrough from "./marks/Strikethrough";
import Link from "./marks/Link";
import WikiWord from "./nodes/WikiWord";
import CheckboxList from "./nodes/CheckboxList";
import CheckboxItem from "./nodes/CheckboxItem";
import CardMention from "./nodes/CardMention";
import Table from "./nodes/Table";
import TableRow from "./nodes/TableRow";
import TableCell from "./nodes/TableCell";
import TableHeader from "./nodes/TableHeader";
import type { ExtensionClass } from "./core/ExtensionManager";

/**
 * Minimal set: a valid document with paragraphs, text, hard breaks and basic inline marks.
 */
export const inlineExtensions: ExtensionClass[] = [
  Doc,
  Paragraph,
  Text,
  HardBreak,
  Bold,
  Italic,
  Code,
  Strikethrough,
  Link,
  WikiWord,
  CardMention,
];

/**
 * Basic set: inline formatting + block structure (headings, lists, quotes, code, rules).
 */
export const basicExtensions: ExtensionClass[] = [
  ...inlineExtensions,
  Heading,
  Blockquote,
  HorizontalRule,
  CodeBlock,
  BulletList,
  OrderedList,
  ListItem,
  CheckboxList,
  CheckboxItem,
  Table,
  TableRow,
  TableCell,
  TableHeader,
];

/**
 * Rich set: everything in basicExtensions plus all embedded card components
 * that have already been registered via CardNodeRegistry. This is the default
 * when RichCardEditor is used without an explicit `extensions` prop.
 */
export const richExtensions: ExtensionClass[] = basicExtensions;
