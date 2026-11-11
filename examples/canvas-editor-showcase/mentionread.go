//go:build js && wasm

package main

import (
	"encoding/json"
	"strings"
)

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
