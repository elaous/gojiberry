//go:build js && wasm

package main

import (
	"encoding/json"
	"strings"
)

// The mentions marked as read are kept in this browser, so that they stay read when the page is opened again. They are
// kept under the title of the document (comment ids are only unique within a document), and a document that is renamed
// shows its mentions again, which errs toward telling the person. Only the keys are kept (a comment id and a reply
// number), never the text; at most mentionsReadMax are kept, the oldest dropped first.

const (
	mentionsReadKey = "showcase:mentions-read"
	mentionsReadMax = 500
	mentionsSep     = "\x1f"
)

func readMentionList() []string {
	var all []string
	if raw := lsGet(mentionsReadKey); raw != "" && json.Unmarshal([]byte(raw), &all) != nil {
		return nil
	}
	return all
}

// syncMentionsRead loads the read marks of the current document when the title is not the one loaded.
func (u *ui) syncMentionsRead() {
	if u.mentionsRead != nil && u.mentionsReadFor == u.title {
		return
	}
	u.mentionsRead, u.mentionsReadFor = map[string]bool{}, u.title
	prefix := u.title + mentionsSep
	for _, k := range readMentionList() {
		if rest, ok := strings.CutPrefix(k, prefix); ok {
			u.mentionsRead[rest] = true
		}
	}
}

// markMentionRead marks a mention (canvas.MentionNotice.Key) as read and keeps the mark.
func (u *ui) markMentionRead(key string) {
	u.syncMentionsRead()
	if u.mentionsRead[key] {
		return
	}
	u.mentionsRead[key] = true
	all := append(readMentionList(), u.title+mentionsSep+key)
	if len(all) > mentionsReadMax {
		all = all[len(all)-mentionsReadMax:]
	}
	if b, err := json.Marshal(all); err == nil {
		lsSet(mentionsReadKey, string(b))
	}
}
