package gen

import _ "embed"

//go:embed gmanifest.json
var manifestJSON []byte

// ManifestJSON returns the raw gmanifest.json bytes embedded at compile time.
func ManifestJSON() []byte { return manifestJSON }

//go:embed static_schema_fragment.json
var staticSchemaFragmentJSON []byte

// StaticSchemaFragmentJSON returns the raw static_schema_fragment.json bytes
// embedded at compile time.
func StaticSchemaFragmentJSON() []byte { return staticSchemaFragmentJSON }
