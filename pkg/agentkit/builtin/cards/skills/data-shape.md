---
id: skill:data-shape
type: skill
name: data-shape
description: Deterministically inspect the structure of a JSON value (types, map keys, array sizes) via a spore script. Use when the user asks to check a value's shape/type/keys, or before writing code against an unfamiliar JSON payload.
when_to_use: |
  - The user says "what shape is this JSON", "inspect this structure", "what fields does this have", "data shape".
  - You have a JSON payload and need exact key names, types, and sizes before generating code or a schema.
  - Precise type introspection matters and eyeballing the JSON is error-prone.
runtime: spore
arguments:
  - input
---

# Data Shape

This is an executable skill (`runtime: spore`): the ```spore fence below is
compiled and run on `agent_skill_use`, and the tool result carries the script's
JSON return value instead of this prose.

Pass the value to inspect as `Args` (any JSON value). The result describes:

- `type` — one of `bool`, `int`, `double`, `string`, `map`, `array`, `null`
- `map` values: ordered `keys` plus `field_types` (per-key type names)
- `array` values: `size` plus `element_types` (count per element type)
- `string` values: `length`
- scalars: their `value`

The script is pure computation: no host callable access, no std, no side
effects.

```spore
fun typeName(v: any): string {
	if v is bool { return "bool" }
	if v is int { return "int" }
	if v is double { return "double" }
	if v is string { return "string" }
	if v is map { return "map" }
	if v is array { return "array" }
	return "null"
}

export fun run(input: any): any {
	var shape: map = {}
	if input is map {
		var m: map = input as map
		shape["type"] = "map"
		shape["size"] = len(m)
		var keys: array = []
		var types: map = {}
		for (k in m) {
			push(keys, k)
			types[k] = typeName(m[k])
		}
		shape["keys"] = keys
		shape["field_types"] = types
		return shape
	}
	if input is array {
		var xs: array = input as array
		shape["type"] = "array"
		shape["size"] = len(xs)
		// Map reads of missing keys throw in spore, so count by walking the
		// closed set of type names and writing each key at most once.
		var names: array = ["bool", "int", "double", "string", "map", "array", "null"]
		var counts: map = {}
		for (nm in names) {
			var n: string = nm as string
			var c: int = 0
			for (x in xs) {
				if typeName(x) == n {
					c = c + 1
				}
			}
			if c > 0 {
				counts[n] = c
			}
		}
		shape["element_types"] = counts
		return shape
	}
	if input is string {
		var s: string = input as string
		var n: int = 0
		for (ch in s) {
			n = n + 1
		}
		shape["type"] = "string"
		shape["length"] = n
		return shape
	}
	shape["type"] = typeName(input)
	if input is bool {
		shape["value"] = input
	}
	if input is int {
		shape["value"] = input
	}
	if input is double {
		shape["value"] = input
	}
	return shape
}
```
