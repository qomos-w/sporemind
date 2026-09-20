//go:build noapk

package mobileassets

// noapk builds embed a tiny placeholder instead of the multi-MB APK so
// lightweight targets (dev-release SDK-embed verification) neither ship the
// APK nor trigger the gradle build chain. ServeDownload 404s on it: the
// placeholder fails the isAPK magic check.

import _ "embed"

//go:embed placeholder.apk
var embedded apkFS