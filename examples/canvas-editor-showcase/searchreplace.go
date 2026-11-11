package main

import (
	"context"
	"errors"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docsearch"
	"github.com/elaous/gojiberry/saver"
)

type replacedDoc struct {
	id    string
	codec docfile.Codec
	old   docfile.Document // as it was before the replacement
	wrote int64            // the Updated stamp the replacement was saved with
}

func applyReplace(ctx context.Context, s *saver.Saver, target, author, title string, plan *docsearch.Plan, keyToID map[string]string, only []string) (done []replacedDoc, conflicts []string, err error) {
	for _, key := range only {
		if err := ctx.Err(); err != nil {
			return done, conflicts, err
		}
		oldBytes, newBytes, ok := plan.Bytes(key)
		id := keyToID[key]
		if !ok || id == "" {
			conflicts = append(conflicts, key)
			continue
		}
		entry, err := s.Get(ctx, target, id)
		if err != nil {
			if errors.Is(err, saver.ErrNotFound) {
				conflicts = append(conflicts, key)
				continue
			}
			return done, conflicts, err
		}
		planned, err := entry.Format.Unmarshal(oldBytes)
		if err != nil {
			conflicts = append(conflicts, key)
			continue
		}
		cur, err := s.Load(ctx, target, id)
		if err != nil {
			return done, conflicts, err
		}
		if cur.Updated != planned.Updated {
			conflicts = append(conflicts, key) // saved since the plan was made
			continue
		}
		repl, err := entry.Format.Unmarshal(newBytes)
		if err != nil {
			conflicts = append(conflicts, key)
			continue
		}
		v, err := s.SaveVersionBy(ctx, target, entry.Format, cur, author)
		if err != nil {
			return done, conflicts, err
		}
		_ = s.SetVersionMeta(ctx, target, v.Key, func(m *saver.VersionMeta) { m.Name = title })
		repl.SetID(id)
		saved, err := s.Save(ctx, target, entry.Format, repl)
		if err != nil {
			return done, conflicts, err
		}
		done = append(done, replacedDoc{id: id, codec: entry.Format, old: cur, wrote: saved.Updated.UnixNano()})
	}
	return done, conflicts, nil
}

func undoReplace(ctx context.Context, s *saver.Saver, target string, done []replacedDoc) (restored, kept int, err error) {
	for _, d := range done {
		if err := ctx.Err(); err != nil {
			return restored, kept, err
		}
		entry, err := s.Get(ctx, target, d.id)
		if err != nil {
			if errors.Is(err, saver.ErrNotFound) {
				kept++
				continue
			}
			return restored, kept, err
		}
		if entry.Updated.UnixNano() != d.wrote {
			kept++
			continue
		}
		if _, err := s.Save(ctx, target, d.codec, d.old); err != nil {
			return restored, kept, err
		}
		restored++
	}
	return restored, kept, nil
}
