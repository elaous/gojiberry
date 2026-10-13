module github.com/elaous/gojiberry/examples/gio-counter-demo

go 1.26.0

replace github.com/elaous/gojiberry => ../..

replace github.com/elaous/gojiberry/xango/gio => ../../xango/gio

require (
	gioui.org v0.10.1
	github.com/elaous/gojiberry v0.0.0-00010101000000-000000000000
	github.com/elaous/gojiberry/xango/gio v0.0.0-00010101000000-000000000000
)

require (
	gioui.org/cmd v0.10.0 // indirect
	gioui.org/shader v1.0.8 // indirect
	github.com/akavel/rsrc v0.10.1 // indirect
	github.com/go-text/typesetting v0.3.4 // indirect
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/image v0.44.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
)

tool gioui.org/cmd/gogio
