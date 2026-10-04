package canvas

// FindFeature adds the find and replace commands. The canvas does the
// searching (TextFlow.Find, ReplaceAll, SetSearch); showing the find bar is the
// app's job (components.FindReplace), so the commands call back into it:
//
//   - Ctrl/Cmd+F opens the bar (OnOpen(false));
//   - Ctrl/Cmd+H opens it with the replace row (OnOpen(true));
//   - F3 / Shift+F3 go to the next and previous match.
type FindFeature struct {
	// OnOpen is called by find.open and find.replace; replace says the replace
	// row was asked for.
	OnOpen func(replace bool)
	// OnStep is called by find.next (1) and find.previous (-1).
	OnStep func(dir int)
}

// Command names.
const (
	CmdFind         Command = "find.open"
	CmdFindReplace  Command = "find.replace"
	CmdFindNext     Command = "find.next"
	CmdFindPrevious Command = "find.previous"
)

// Name implements Feature.
func (FindFeature) Name() string { return "find" }

// Install implements Feature.
func (ff FindFeature) Install(r *Registry) {
	open := func(replace bool) func(*CommandContext) bool {
		return func(c *CommandContext) bool {
			if ff.OnOpen == nil {
				return false
			}
			ff.OnOpen(replace)
			return true
		}
	}
	step := func(dir int) func(*CommandContext) bool {
		return func(c *CommandContext) bool {
			if ff.OnStep == nil {
				return false
			}
			ff.OnStep(dir)
			return true
		}
	}
	r.AddGlobalCommand(CmdFind, "Find…", "Mod+F", open(false))
	r.AddGlobalCommand(CmdFindReplace, "Find and replace…", "Mod+H", open(true))
	r.AddGlobalCommand(CmdFindNext, "Find next", "F3", step(1))
	r.AddGlobalCommand(CmdFindPrevious, "Find previous", "Shift+F3", step(-1))
}
