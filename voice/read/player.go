package read

// Player reads the pieces of a document one after the other over an Engine: it starts the next when one ends, marks
// the piece and the word being said, and pauses, skips and stops at piece borders. It holds no engine of its own: the
// page sets Engine (and may change it between readings).
type Player struct {
	Engine Engine
	// Voice gives the ID of the voice to use for a language ("" for the engine's pick).
	Voice func(lang string) string
	// Rate gives the speed (1 normal).
	Rate func() float64
	// OnPiece is called when a piece starts; reveal says to scroll to it.
	OnPiece func(c Chunk, reveal bool)
	// OnWord is called with the stream range of the word being said.
	OnWord func(c Chunk, from, to int)
	// OnChange is called whenever what the Player shows changed (to draw the bar again).
	OnChange func()
	// OnStop is called when reading ends, whether it ran out of pieces or was stopped.
	OnStop func()
	// OnError is called when a piece could not be said; reading stops.
	OnError func(err error)

	chunks  []Chunk
	pos     int
	playing bool
	paused  bool
	gen     int
	cur     Speech
	kind    string
}

// Start reads the pieces from the first, after stopping any reading.
func (p *Player) Start(chunks []Chunk) {
	p.Stop()
	if len(chunks) == 0 || p.Engine == nil || !p.Engine.Available() {
		return
	}
	p.chunks, p.pos, p.playing, p.paused = chunks, 0, true, false
	p.speak()
}

// Playing says a reading is going on (paused or not).
func (p *Player) Playing() bool { return p.playing }

// Paused says the reading is held.
func (p *Player) Paused() bool { return p.paused }

// Current is the piece being said.
func (p *Player) Current() (Chunk, bool) {
	if !p.playing || p.pos < 0 || p.pos >= len(p.chunks) {
		return Chunk{}, false
	}
	return p.chunks[p.pos], true
}

// Kind is what is speaking, as the engine told it (the Kind constants), "" when not known.
func (p *Player) Kind() string { return p.kind }

// Stop ends the reading.
func (p *Player) Stop() {
	was := p.playing
	p.gen++
	if p.cur != nil {
		p.cur.Cancel()
		p.cur = nil
	}
	p.playing, p.paused, p.kind = false, false, ""
	p.chunks, p.pos = nil, 0
	if was && p.OnStop != nil {
		p.OnStop()
	}
	p.changed()
}

// Pause holds the reading, or continues it when it is held.
func (p *Player) Pause() {
	if !p.playing || p.cur == nil {
		return
	}
	if p.paused {
		p.cur.Resume()
	} else {
		p.cur.Pause()
	}
	p.paused = !p.paused
	p.changed()
}

// Skip moves to the previous (-1) or next (1) piece.
func (p *Player) Skip(by int) {
	if !p.playing {
		return
	}
	pos := max(p.pos+by, 0)
	p.gen++
	if p.cur != nil {
		p.cur.Cancel()
		p.cur = nil
	}
	p.pos, p.paused = pos, false
	p.speak()
}

func (p *Player) changed() {
	if p.OnChange != nil {
		p.OnChange()
	}
}

func (p *Player) speak() {
	if !p.playing || p.pos >= len(p.chunks) {
		p.Stop()
		return
	}
	gen := p.gen
	c := p.chunks[p.pos]
	u := Utterance{Text: c.Text, Lang: c.Lang, Rate: 1}
	if p.Voice != nil {
		u.Voice = p.Voice(c.Lang)
	}
	if p.Rate != nil {
		u.Rate = p.Rate()
	}
	p.kind = ""
	if p.OnPiece != nil {
		p.OnPiece(c, true)
	}
	live := func() bool { return gen == p.gen && p.playing }
	speech := p.Engine.Speak(u, Handler{
		OnStart: func(kind string) {
			if live() {
				p.kind = kind
				p.changed()
			}
		},
		OnWord: func(index, length int) {
			if !live() || p.OnWord == nil {
				return
			}
			if from, to, ok := c.WordAt(index, length); ok {
				p.OnWord(c, from, to)
			}
		},
		OnEnd: func() {
			if live() {
				p.pos++
				p.speak()
			}
		},
		OnError: func(err error) {
			if !live() {
				return
			}
			p.Stop()
			if p.OnError != nil {
				p.OnError(err)
			}
		},
	})
	if gen == p.gen { // the engine may have called back before it returned
		p.cur = speech
	}
	p.changed()
}
