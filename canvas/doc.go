// Package canvas holds components drawn onto an HTML <canvas> with
// Context2D, rather than built from DOM elements.
//
// Everything here is split in two so it can be tested natively:
//
//   - The model, layout, hit-testing, editing state machine and drawing
//     code are plain Go with no build tag. Drawing goes through the
//     small Painter interface, which *html.Context2D (domrt/html)
//     already satisfies, so tests can substitute a recording fake.
//   - The browser glue (TableView.Bind) is js/wasm-only and does nothing
//     but translate DOM mouse/keyboard events into calls on the
//     native-testable half.
//
// The first component is Table / TableView, an Obsidian-style editable
// table: click a cell to edit it, Tab/Enter/arrow keys move between
// cells, and a "+" bar on the right edge and bottom edge adds a column
// or row.
package canvas
