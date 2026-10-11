package components

import (
	"slices"
	"strings"
	"time"
)

// Collaboration cues are visual (a coloured cursor, an avatar that appears). CollabAnnouncer turns what
// changed in a shared session into short sentences for a live region (Announcer): people joining and
// leaving, the connection dropping and coming back, a sharing problem. Changes are grouped ("Ana and Bo
// joined") and spaced (at most one sentence per Every), so a busy session does not talk over the person.
type CollabAnnouncer struct {
	// Level is how much is said: CollabOff nothing, CollabImportant the connection and problems, CollabAll
	// that and the people who come and go (the default, "").
	Level string
	// Every is the least time between two sentences (2 seconds if 0). Changes that come sooner are held and
	// said together at the next Update after it.
	Every time.Duration

	started, wasOnline bool
	people             map[string]int // names present, by how many have that name
	online             bool
	problem            string
	joined, left       []string
	comments           []string // authors of comments others added, not yet said
	cause              string   // a held connection sentence: "online", "offline" or ""
	problemSaid        string
	last               time.Time
}

// The levels of CollabAnnouncer.Level.
const (
	CollabAll       = ""
	CollabImportant = "important"
	CollabOff       = "off"
)

// NoteComment records that someone else added a comment; it is said, with the next sentence, at level
// CollabAll ("Bo commented." or "3 new comments.").
func (a *CollabAnnouncer) NoteComment(author string) {
	if a.Level == CollabAll {
		a.comments = append(a.comments, author)
	}
}

// Update takes the session as it is now (online or not, the names of the other people, the problem if
// sharing is not working) and returns the sentence to say now, or "". The first call only remembers the state.
func (a *CollabAnnouncer) Update(now time.Time, online bool, people []string, problem string) string {
	cur := map[string]int{}
	for _, n := range people {
		cur[n]++
	}
	if !a.started {
		a.started, a.people, a.online, a.wasOnline, a.problem = true, cur, online, online, problem
		return ""
	}
	// what changed since the last call
	for n, c := range cur {
		for i := a.people[n]; i < c; i++ {
			a.joined = append(a.joined, n)
		}
	}
	for n, c := range a.people {
		for i := cur[n]; i < c; i++ {
			a.left = append(a.left, n)
		}
	}
	if online != a.online {
		switch {
		case online && !a.wasOnline:
			// the first time it is connected is not news
		case online:
			a.cause = "online"
		default:
			a.cause = "offline"
		}
	}
	a.wasOnline = a.wasOnline || online
	a.people, a.online = cur, online
	if problem != a.problem {
		a.problem = problem
	}
	if a.Level == CollabOff {
		a.joined, a.left, a.cause, a.comments = nil, nil, "", nil
		a.problemSaid = problem
		return ""
	}
	// a person who joined and left before anything was said cancels out
	for i := 0; i < len(a.joined); i++ {
		if j := slices.Index(a.left, a.joined[i]); j >= 0 {
			a.left = slices.Delete(a.left, j, j+1)
			a.joined = slices.Delete(a.joined, i, i+1)
			i--
		}
	}
	every := a.Every
	if every <= 0 {
		every = 2 * time.Second
	}
	if now.Sub(a.last) < every {
		return ""
	}
	var parts []string
	switch a.cause {
	case "online":
		parts = append(parts, tr("collab.online", "Connected again."))
	case "offline":
		parts = append(parts, tr("collab.offline", "The connection to the shared document was lost."))
	}
	if a.problem != a.problemSaid {
		if a.problem != "" {
			parts = append(parts, trf("collab.problem", "Sharing problem: %s", a.problem))
		} else if a.problemSaid != "" {
			parts = append(parts, tr("collab.problemGone", "Sharing works again."))
		}
		a.problemSaid = a.problem
	}
	if a.Level == CollabAll {
		if len(a.joined) > 0 {
			parts = append(parts, peopleSentence(a.joined, "collab.joined", "%s joined.", "collab.joinedMany", "%d people joined: %s."))
		}
		if len(a.left) > 0 {
			parts = append(parts, peopleSentence(a.left, "collab.left", "%s left.", "collab.leftMany", "%d people left: %s."))
		}
	}
	if a.Level == CollabAll && len(a.comments) > 0 {
		if len(a.comments) == 1 {
			parts = append(parts, trf("collab.commented", "%s commented.", a.comments[0]))
		} else {
			parts = append(parts, trn("collab.newComments", "%d new comment.", "%d new comments.", len(a.comments)))
		}
	}
	a.joined, a.left, a.cause, a.comments = nil, nil, "", nil
	if len(parts) == 0 {
		return ""
	}
	a.last = now
	return strings.Join(parts, " ")
}

// Pending reports whether something has changed that has not been said yet (held by the spacing), so a
// host can call Update again a little later.
func (a *CollabAnnouncer) Pending() bool {
	return a.Level != CollabOff && (a.cause != "" || a.problem != a.problemSaid || (a.Level == CollabAll && len(a.joined)+len(a.left)+len(a.comments) > 0))
}

// peopleSentence names up to three people, and counts more.
func peopleSentence(names []string, one, oneDef, many, manyDef string) string {
	slices.Sort(names)
	if len(names) == 1 {
		return trf(one, oneDef, names[0])
	}
	shown := names
	if len(shown) > 3 {
		shown = shown[:3]
	}
	list := strings.Join(shown, ", ")
	if len(names) > 3 {
		list += ", …"
	}
	return trf(many, manyDef, len(names), list)
}
