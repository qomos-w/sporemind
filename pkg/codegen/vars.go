package codegen

// embeddedSDKZip and embeddedSDKChecksum live in the release-tagged file pair:
//   - sdk_embed_release.go embeds the packed SDK source zip (make
//     build-sdk-asset output) so release builds can materialize the SDK on
//     user machines with no sporemind checkout.
//   - sdk_embed_dev.go keeps both empty in dev builds, where findDevSDK
//     resolves the SDK from the checkout.
// This file keeps the package documentation in one place.
