# Keyboard-only use of the showcase editor

Everything in the editor can be done without a pointer. This is the inventory of how each part is reached, the gaps
that were found by walking it, and the tests that keep it so (roadmap item 59).

## How each part is reached

| Part | Keys | Tested by |
| --- | --- | --- |
| The page | The first two Tab stops are the skip links: *Skip to the document* and *Skip to the side panel* (Enter moves the focus there). The Tab order is one clean cycle: skip links, document title, menu bar, mode button, the side panel's tabs and panel, the document, and round again; no stop on anything that cannot be seen; Shift+Tab walks it back. | `TestTabOrderIsACleanCycle`, `TestSkipLinksAreTheFirstStopAndMoveTheFocus` |
| Menus | The menu bar is one Tab stop; Left and Right move among the menus, Down opens one and goes to its first entry, Up and Down walk the entries (wrapping), Home and End go to the ends, Right enters a submenu and Left leaves it, Enter or Space chooses, Escape closes and gives the focus back to the menu button. All 218 entries of the showcase's menus are reached. | `TestEveryMenuEntryIsReachableByKeyboard`, `components` `TestMenu*` |
| Edit mode | The mode button is in the Tab order (Enter switches). In edit mode the keyboard arriving in the editor (Tab, a skip link) starts editing where the caret last was, with no click. | `TestTheEditorWithoutAPointer`, `canvas` `TestFocusInStartsEditing` |
| The text | Typing, the arrow keys, Home and End, Shift to select, and the shortcuts listed in the Keys tab (below). Vertical text turns the arrows to follow the columns; right-to-left text moves by what is seen. | `TestTypingSpeaksFinishedWords` and the e2e tests of each |
| The side panel | The tabs are a tab list: Tab reaches the selected one, Left and Right (Home, End) move among them and select. Inside a panel every control is a native button, input or select (one Tab stop each); option lists, grids and chips have the models in [Keyboard models](#keyboard-models). | `TestTheEditorWithoutAPointer`, `TestSymbolPickerArrowKeys`, axe over 21 tabs |
| Dialogs | The browser's own prompt dialogs take the keyboard; the components' modals trap the focus while open and return it to what opened them. | `TestModalKeyboard`, `TestDialogFocusIsTrappedAndReturned` |
| Save and open | The name field, the save button and the list entries are native controls in the Tab order. | `TestTheEditorWithoutAPointer` |

## The cheat sheet and remapping

The Keys tab (Help > Keyboard shortcuts) lists every command with its chord, from the one table the editor itself
uses (`stage.Keymap`). A chord can be changed there or in Preferences, and the change is kept in this browser
(`showcase:keys`, `TestChangedShortcutsAreKept`); *Reset* goes back to the defaults. A menu entry shows the chord that is
bound now.

## Keyboard models

One model per kind of widget, so that what works in one works in the others:

* a button that opens a list (Dropdown, FontSelector): Down or Up opens and goes to an option; Up, Down, Home and End
  move; Enter or Space chooses; Escape closes and returns to the button; Tab leaves (`components/listboxkeys.go`);
* a menu or menu bar: above;
* a tab list: Left, Right, Home and End (`components/tablist.go`);
* a grid, row or column of choices (symbols, category chips, the style gallery): one Tab stop; the arrows move by what
  is seen, Home and End go to the first and last (`components/gridkeys.go`);
* the suggestion list: a combobox; the focus stays in the editor's input and Down, Up, Enter and Escape drive the list;
* the colour and date pickers are the browser's inputs with the browser's keys.

## Gaps found by walking it, and fixed

* The debug dump of the editor's state (`#state`) was a scrollable region, so Chrome gave it a Tab stop: a keyboard
  user landed on an invisible block of JSON.
* A submenu was walked two entries at a time: the key handled in the submenu went on to the entry that holds it,
  which moved the focus again.
* In edit mode the keyboard could not start editing: the editor only began on a click, so typing after Tab or a skip
  link went nowhere.
* A hundred symbols took a hundred Tab presses (now one stop and the arrow keys).
