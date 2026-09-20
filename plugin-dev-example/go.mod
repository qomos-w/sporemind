module app.authenticator

go 1.27.0

require github.com/qomos-w/sporemind-plugin-sdk v0.0.0

require (
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
)

replace github.com/qomos-w/sporemind-plugin-sdk => ./vendor-sdk
