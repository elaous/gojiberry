package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (sh *Shell) mkdir(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mkdir path [more]")
	}
	for _, a := range args {
		t, err := sh.tree(ctx)
		if err != nil {
			return err
		}
		dir, name := splitPath(strings.TrimRight(a, "/"))
		if name == "" || name == "." || name == ".." {
			return fmt.Errorf("console: %q is not a name for a folder", a)
		}
		parent, err := sh.folder(t, dir)
		if err != nil {
			return err
		}
		if _, err := t.child(parent, name); err == nil {
			return fmt.Errorf("console: %q already exists", a)
		}
		if err := sh.act("create folder "+a, func() error {
			_, err := sh.Saver.CreateFolder(ctx, sh.Target, parent, name)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (sh *Shell) rmdir(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rmdir path [more]")
	}
	for _, a := range args {
		t, err := sh.tree(ctx)
		if err != nil {
			return err
		}
		id, err := sh.folder(t, a)
		if err != nil || id == "" {
			return fmt.Errorf("console: no folder %q", a)
		}
		if err := sh.folderEmpty(ctx, t, id, a); err != nil {
			return err
		}
		if err := sh.act("remove folder "+a, func() error {
			if err := sh.Saver.DeleteFolder(ctx, sh.Target, id); err != nil {
				return err
			}
			if sh.cwd == id { // do not stay in a folder that is gone
				sh.cwd = t.byID[id].Parent
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (sh *Shell) folderEmpty(ctx context.Context, t *tree, id, label string) error {
	for _, f := range t.folders {
		if f.Parent == id {
			return fmt.Errorf("console: %s has folders in it", label)
		}
	}
	docs, err := sh.Saver.List(ctx, sh.Target)
	if err != nil {
		return err
	}
	for _, d := range docs {
		if d.Folder == id {
			return fmt.Errorf("console: %s has documents in it", label)
		}
	}
	return nil
}

func (sh *Shell) mv(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: mv source... dest[/]")
	}
	src, dest := args[:len(args)-1], args[len(args)-1]
	if dest == "" {
		return errors.New("console: the destination is empty")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	things, err := sh.matchAll(ctx, t, src, false)
	if err != nil {
		return err
	}
	if id, ferr := sh.folder(t, dest); ferr == nil && (strings.HasSuffix(dest, "/") || len(things) > 1 || destIsFolder(t, id, things)) {
		return sh.moveInto(ctx, things, id, dest)
	} else if strings.HasSuffix(dest, "/") {
		return ferr
	}
	if len(things) != 1 {
		return fmt.Errorf("console: %d things match; the destination must be a folder (end it with /)", len(things))
	}
	th := things[0]
	dir, name := splitPath(dest)
	if name == "." || name == ".." {
		return fmt.Errorf("console: %q is not a name", name)
	}
	parent, err := sh.folder(t, dir)
	if err != nil {
		return err
	}
	if th.folder != nil {
		if err := sh.act(fmt.Sprintf("move folder %s to %s", th.label(), dest), func() error {
			if parent != th.folder.Parent {
				if err := sh.Saver.MoveFolder(ctx, sh.Target, th.folder.ID, parent); err != nil {
					return err
				}
			}
			if name != "" && name != th.folder.Name {
				return sh.Saver.RenameFolder(ctx, sh.Target, th.folder.ID, name)
			}
			return nil
		}); err != nil {
			return err
		}
		return nil
	}
	return sh.act(fmt.Sprintf("rename %s to %s", th.label(), dest), func() error {
		if name != "" && name != th.doc.Name {
			if err := sh.Saver.Rename(ctx, sh.Target, th.doc.ID, name); err != nil {
				return err
			}
		}
		if parent != th.doc.Folder {
			return sh.Saver.Move(ctx, sh.Target, parent, th.doc.ID)
		}
		return nil
	})
}

func destIsFolder(t *tree, id string, things []thing) bool {
	for _, th := range things {
		if th.folder != nil && th.folder.ID == id {
			return false
		}
	}
	return true
}

func (sh *Shell) moveInto(ctx context.Context, things []thing, folder, label string) error {
	var docs []string
	for _, th := range things {
		if th.folder != nil {
			th := th
			if err := sh.act(fmt.Sprintf("move folder %s into %s", th.label(), label), func() error {
				return sh.Saver.MoveFolder(ctx, sh.Target, th.folder.ID, folder)
			}); err != nil {
				return err
			}
			continue
		}
		docs = append(docs, th.doc.ID)
	}
	if len(docs) == 0 {
		return nil
	}
	names := make([]string, 0, len(things))
	for _, th := range things {
		if th.doc != nil {
			names = append(names, th.name)
		}
	}
	return sh.act(fmt.Sprintf("move %s into %s", strings.Join(names, ", "), label), func() error {
		return sh.Saver.Move(ctx, sh.Target, folder, docs...)
	})
}

func (sh *Shell) cp(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: cp source dest[/]")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	_, got, err := sh.match(ctx, t, args[0], false)
	if err != nil {
		return err
	}
	var docs []thing
	for _, th := range got {
		if th.doc != nil {
			docs = append(docs, th)
		}
	}
	if len(docs) == 0 {
		return fmt.Errorf("console: no document matches %q", args[0])
	}
	dir, name := splitPath(args[1])
	parent, err := sh.folder(t, dir)
	if err != nil {
		return err
	}
	if name != "" && len(docs) > 1 {
		return errors.New("console: several documents match; give a folder (ending in /) to copy them into")
	}
	for _, th := range docs {
		th := th
		newName := "Copy of " + th.name
		if name != "" {
			newName = name
		}
		if err := sh.act(fmt.Sprintf("copy %s to %s%s", th.name, dir, newName), func() error {
			e, err := sh.Saver.Duplicate(ctx, sh.Target, th.doc.ID, newName)
			if err != nil {
				return err
			}
			if parent != th.doc.Folder {
				return sh.Saver.Move(ctx, sh.Target, parent, e.ID)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (sh *Shell) tag(ctx context.Context, args []string) error {
	var add, drop, pats []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "+") && len(a) > 1:
			add = append(add, a[1:])
		case strings.HasPrefix(a, "-") && len(a) > 1:
			drop = append(drop, a[1:])
		default:
			pats = append(pats, a)
		}
	}
	if len(pats) == 0 || len(add)+len(drop) == 0 {
		return errors.New("usage: tag [+tag|-tag]... pattern [pattern ...]")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	things, err := sh.matchAll(ctx, t, pats, false)
	if err != nil {
		return err
	}
	for _, th := range things {
		if th.doc == nil {
			continue
		}
		th := th
		var tags []string
		for _, x := range th.doc.Tags {
			if !hasFold(drop, x) {
				tags = append(tags, x)
			}
		}
		for _, x := range add {
			if !hasFold(tags, x) {
				tags = append(tags, x)
			}
		}
		if err := sh.act(fmt.Sprintf("tag %s: [%s]", th.name, strings.Join(tags, " ")), func() error {
			return sh.Saver.SetTags(ctx, sh.Target, th.doc.ID, tags)
		}); err != nil {
			return err
		}
	}
	return nil
}

func hasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (sh *Shell) docsOf(things []thing) (ids, names []string) {
	for _, th := range things {
		if th.doc != nil {
			ids, names = append(ids, th.doc.ID), append(names, th.name)
		}
	}
	return
}

func (sh *Shell) rm(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rm pattern...  (to the trash; purge deletes for good)")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	things, err := sh.matchAll(ctx, t, args, false)
	if err != nil {
		return err
	}
	for _, th := range things {
		if th.folder != nil {
			sh.warnf("skipped %s: a folder (rmdir removes an empty one)\n", th.label())
		}
	}
	ids, names := sh.docsOf(things)
	if len(ids) == 0 {
		return errors.New("console: only folders match; nothing to trash")
	}
	return sh.act("trash "+strings.Join(names, ", "), func() error { return sh.Saver.Trash(ctx, sh.Target, ids...) })
}

func (sh *Shell) purge(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: purge pattern...  (deletes from the trash for good)")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	things, err := sh.matchAll(ctx, t, args, true)
	if err != nil {
		return err
	}
	ids, names := sh.docsOf(things)
	desc := "delete for good " + strings.Join(names, ", ")
	if !sh.DryRun {
		shown := names
		if len(shown) > 10 {
			shown = shown[:10]
		}
		for _, n := range shown {
			sh.printf("  %s\n", n)
		}
		if len(names) > len(shown) {
			sh.printf("  ... and %d more\n", len(names)-len(shown))
		}
	}
	if !sh.DryRun && !sh.ask(fmt.Sprintf("Delete %d documents from the trash for good? This cannot be undone.", len(ids))) {
		return sh.declined()
	}
	return sh.act(desc, func() error { return sh.Saver.Purge(ctx, sh.Target, ids...) })
}

func (sh *Shell) restore(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: restore pattern [more]")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	things, err := sh.matchAll(ctx, t, args, true)
	if err != nil {
		return err
	}
	ids, names := sh.docsOf(things)
	return sh.act("restore "+strings.Join(names, ", "), func() error { return sh.Saver.Restore(ctx, sh.Target, ids...) })
}

func (sh *Shell) trash(ctx context.Context) error {
	docs, err := sh.Saver.Trashed(ctx, sh.Target)
	if err != nil {
		return err
	}
	for _, d := range docs {
		sh.printf("%s  (trashed %s)\n", d.Name, d.Trashed.Format("2006-01-02 15:04"))
	}
	return nil
}
