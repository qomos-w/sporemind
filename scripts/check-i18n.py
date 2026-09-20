#!/usr/bin/env python3
"""Basic i18n verification for Sporemind.

This script performs two checks:
1. Locale JSON parity: every key in zh-CN.json must exist in en-US.json and vice versa.
2. Scans web/src TS/TSX files for Chinese characters that do not appear in any locale
   value, which usually indicates a newly introduced untranslated string.

It intentionally does not scan Go backend files because most Chinese strings there are
LLM prompts or developer comments, both of which are out of scope for localization.
"""

import json
import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WEB_SRC = ROOT / "web" / "src"
LOCALES = ROOT / "web" / "src" / "i18n" / "locales"


def load_locale(name: str) -> dict[str, str]:
    with open(LOCALES / name, "r", encoding="utf-8") as f:
        return json.load(f)


def check_json_parity() -> list[str]:
    zh = load_locale("zh-CN.json")
    en = load_locale("en-US.json")
    errors: list[str] = []
    for key in zh:
        if key not in en:
            errors.append(f"en-US missing key: {key}")
    for key in en:
        if key not in zh:
            errors.append(f"zh-CN missing key: {key}")
    return errors


def collect_locale_values() -> set[str]:
    values: set[str] = set()
    for name in ("zh-CN.json", "en-US.json"):
        for v in load_locale(name).values():
            values.add(v)
    return values


def strip_comments(line: str) -> str:
    # Very rough comment stripping for TS/TSX single-line comments.
    # Does not handle JSX comments or block comments spanning lines.
    return re.sub(r"//.*", "", line)


def check_untranslated_strings() -> list[str]:
    locale_values = collect_locale_values()
    errors: list[str] = []
    chinese_re = re.compile(r"[\u4e00-\u9fff]{2,}")

    for path in WEB_SRC.rglob("*"):
        if path.suffix not in (".ts", ".tsx"):
            continue
        with open(path, "r", encoding="utf-8") as f:
            for i, raw in enumerate(f, 1):
                line = strip_comments(raw)
                for match in chinese_re.findall(line):
                    # Skip import paths and obvious false positives.
                    if "@qomos" in line or "/" in match:
                        continue
                    found = any(match in v for v in locale_values)
                    if not found:
                        errors.append(f"{path.relative_to(ROOT)}:{i}: untranslated '{match}'")
    return errors


def main() -> int:
    strict = "--strict" in sys.argv
    parity_errors = check_json_parity()
    untranslated_errors = check_untranslated_strings()

    if parity_errors:
        print("Locale JSON parity errors:")
        for e in parity_errors:
            print(f"  {e}")
    else:
        print("Locale JSON parity: OK")

    if untranslated_errors:
        print(f"Found {len(untranslated_errors)} potential untranslated strings:")
        for e in untranslated_errors[:50]:
            print(f"  {e}")
        if len(untranslated_errors) > 50:
            print(f"  ... and {len(untranslated_errors) - 50} more")
    else:
        print("No obvious untranslated Chinese strings in web/src.")

    if parity_errors:
        return 1
    if strict and untranslated_errors:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
