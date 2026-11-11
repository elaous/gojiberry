package main

import (
	"log"
	"os"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/examples/gio-counter-demo/shared"
	"github.com/elaous/gojiberry/xango"
	"github.com/elaous/gojiberry/xango/gio"
)

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("gojiberry gio counter demo"))
		if err := run(w); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

func run(w *app.Window) error {
	th := material.NewTheme()
	renderer := gio.New()

	count := 0
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			node := shared.Counter(shared.CounterProps{
				Count: count,
				OnInc: func(xango.Event) { count++ },
				OnDec: func(xango.Event) { count-- },
			})
			renderer.Layout(gtx, th, node)
			e.Frame(gtx.Ops)
		}
	}
}
