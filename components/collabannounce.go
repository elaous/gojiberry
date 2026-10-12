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
	replies            []string // authors of replies others added, not yet said
	resolved           int      // threads others resolved, not yet said
	merged, rejected   int      // changes merged after being offline / refused by the server, not yet said
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

// NoteReply records that someone else replied to a comment; it is said at level CollabAll ("Bo replied."
// or "3 new replies.").
func (a *CollabAnnouncer) NoteReply(author string) {
	if a.Level == CollabAll {
		a.replies = append(a.replies, author)
	}
}

// NoteResolved records that someone else resolved a comment thread ("A comment thread was resolved." or
// "3 comment threads were resolved."), said at level CollabAll.
func (a *CollabAnnouncer) NoteResolved() {
	if a.Level == CollabAll {
		a.resolved++
	}
}

// NoteMerged records that n changes made while offline met the shared document when the connection came
// back ("3 changes were merged."). It is said at the levels CollabImportant and CollabAll: it says that
// work was kept, which the person needs to know whoever else is there.
func (a *CollabAnnouncer) NoteMerged(n int) {
	if a.Level != CollabOff && n > 0 {
		a.merged += n
	}
}

// NoteRejected records that n of the person's changes could not be applied (the server refused them: "A
// change could not be applied."). Said at CollabImportant and CollabAll.
func (a *CollabAnnouncer) NoteRejected(n int) {
	if a.Level != CollabOff && n > 0 {
		a.rejected += n
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
		a.joined, a.left, a.cause, a.comments, a.replies, a.resolved, a.merged, a.rejected = nil, nil, "", nil, nil, 0, 0, 0
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
	if a.merged > 0 {
		parts = append(parts, trn("collab.merged", "%d change was merged.", "%d changes were merged.", a.merged))
	}
	if a.rejected == 1 {
		parts = append(parts, tr("collab.rejectedOne", "A change could not be applied."))
	} else if a.rejected > 1 {
		parts = append(parts, trf("collab.rejectedMany", "%d changes could not be applied.", a.rejected))
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
	if a.Level == CollabAll && len(a.replies) > 0 {
		if len(a.replies) == 1 {
			parts = append(parts, trf("collab.replied", "%s replied.", a.replies[0]))
		} else {
			parts = append(parts, trn("collab.newReplies", "%d new reply.", "%d new replies.", len(a.replies)))
		}
	}
	if a.Level == CollabAll && a.resolved > 0 {
		parts = append(parts, trn("collab.resolved", "%d comment thread was resolved.", "%d comment threads were resolved.", a.resolved))
	}
	a.joined, a.left, a.cause, a.comments, a.replies, a.resolved, a.merged, a.rejected = nil, nil, "", nil, nil, 0, 0, 0
	if len(parts) == 0 {
		return ""
	}
	a.last = now
	return strings.Join(parts, " ")
}

// Pending reports whether something has changed that has not been said yet (held by the spacing), so a
// host can call Update again a little later.
func (a *CollabAnnouncer) Pending() bool {
	return a.Level != CollabOff && (a.cause != "" || a.merged+a.rejected > 0 || a.problem != a.problemSaid || (a.Level == CollabAll && len(a.joined)+len(a.left)+len(a.comments)+len(a.replies)+a.resolved > 0))
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
