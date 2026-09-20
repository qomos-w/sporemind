//go:build !noapk

package mobileassets

// Default builds embed the real APK (produced by make copy-mobile-asset into
// pkg/mobileassets/sporemind.apk). The file must exist at compile time.

import _ "embed"

//go:embed sporemind.apk
var embedded apkFS