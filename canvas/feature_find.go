package canvas

type FindFeature struct {
	// OnOpen runs on find.open and find.replace; replace marks the latter.
	OnOpen func(replace bool)
	// OnStep is called by find.next (1) and find.previous (-1).
	OnStep func(dir int)
}

const (
	CmdFind         Command = "find.open"
	CmdFindReplace  Command = "find.replace"
	CmdFindNext     Command = "find.next"
	CmdFindPrevious Command = "find.previous"
)

func (FindFeature) Name() string { return "find" }

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
