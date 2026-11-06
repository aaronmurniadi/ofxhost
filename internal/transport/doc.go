// Package transport carries the services to the user: the window, the JS
// bindings and the frame server.
//
// It is the top of the layer graph. The only place that imports a UI framework,
// checked by tools/layerguard, and the only place that knows the window exists.
//
// Subpackages:
//
//	desktop/    the Wails application, the window, menus and native dialogs
//	binds/      the JS-facing types: DTO in, DTO out, no logic
//	frames/     the loopback HTTP handler that serves preview pixels (M2)
//	events/     event names and payload types in one place (M3)
//
// A binding converts and nothing else. It never reaches past the service layer,
// and it never returns pixels: a preview reaches the WebView as an 8-bit sRGB
// image from the frame server, because the JSON bridge is for small payloads.
package transport
