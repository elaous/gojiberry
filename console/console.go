// Package console is a command line for the work that is clumsy with a mouse: bulk rename,
// move and tag by pattern, import and export of folders, packs, repair and rebuild of a
// catalog. It drives a saver.Saver, so it works on any store the saver does (files, SQLite,
// a docstore server, the browser's storage through the page).
//
//	ls [-l] [pattern...]        list the documents and folders here
//	cd path | pwd | mkdir path... | rmdir path...
//	mv source... dest[/]        move into a folder, or rename one thing
//	cp source dest[/]           copy a document (into a folder if dest ends in /)
//	tag [+tag|-tag]... pattern...
//	rm pattern...               to the trash;  purge pattern...  deletes for good (asks)
//	restore pattern...          out of the trash;  trash  lists it
//	import [-d dir] file...     bring in files (.docx, .md, .txt, .open, ...)
//	export [-f format] [-o dir] [--force] pattern...
//	pack [--force] out.opendoc [folder]   one .opendoc pack;  unpack file.opendoc [dir]
//	check | repair | rebuild    the integrity tools;  usage  how full the store is
//
// A pattern is a name with * (any run of characters) and ? (any one character), in any case,
// optionally under a folder path ("Work/Reports/Q*"); every other character, [ ] and \
// included, stands for itself. With DryRun every command that would change something says what it
// would do and does nothing; a command that destroys (purge, rebuild) asks first unless Yes.
package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
)

// Shell runs console commands on one target of a Saver.
type Shell struct {
	Saver  *saver.Saver
	Target string
	// In answers questions; Out and Err get the output and the messages.
	In       io.Reader
	Out, Err io.Writer
	// DryRun says what would change and changes nothing; Yes answers every question yes.
	DryRun, Yes bool
	// Script says the commands come from a file: Interact stops at the first command that
	// fails and returns its error, and a question is never read from the script (it is
	// answered no unless Yes is set).
	Script bool
	// Registry reads and writes the files import and export handle; nil is the default set
	// (JSON, protobuf, Markdown, text, .docx, .pdf, HTML, ...).
	Registry *transfer.Registry
	// Format is how imported documents are stored; nil is JSON.
	Format docfile.Codec

	cwd string // the folder ID being worked in, "" for the top
	in  *bufio.Reader
}

// New makes a Shell on target.
func New(s *saver.Saver, target string, in io.Reader, out, errw io.Writer) *Shell {
	return &Shell{Saver: s, Target: target, In: in, Out: out, Err: errw}
}

// Cwd is the ID of the folder commands work in ("" for the top); SetCwd moves there. A host
// that shows folders itself keeps the two in step with them.
func (sh *Shell) Cwd() string { return sh.cwd }

// SetCwd sets the folder commands work in.
func (sh *Shell) SetCwd(id string) { sh.cwd = id }

func (sh *Shell) registry() *transfer.Registry {
	if sh.Registry == nil {
		r := transfer.Default()
		export.Register(r)
		sh.Registry = r
	}
	return sh.Registry
}

func (sh *Shell) printf(format string, a ...any) { _, _ = fmt.Fprintf(sh.Out, format, a...) }
func (sh *Shell) warnf(format string, a ...any)  { _, _ = fmt.Fprintf(sh.Err, format, a...) }

// Split breaks a command line into words: spaces separate them, single and double quotes
// group. A backslash outside single quotes escapes a space, a quote or another backslash;
// before any other character it stays as it is (so C:\docs\a.md and a pattern with \ in it
// pass through). A line cannot end in a lone backslash.
func Split(line string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in, quote := false, rune(0)
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\' && quote != '\'':
			if i+1 == len(rs) {
				return nil, errors.New("console: a line cannot end with a backslash")
			}
			n := rs[i+1]
			if n == '\\' || n == '\'' || n == '"' || (quote == 0 && unicode.IsSpace(n)) {
				cur.WriteRune(n)
				i++
			} else {
				cur.WriteRune(r)
			}
			in = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case unicode.IsSpace(r):
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("console: a quote is not closed")
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

// Run runs one command line.
func (sh *Shell) Run(ctx context.Context, line string) error {
	words, err := Split(line)
	if err != nil || len(words) == 0 {
		return err
	}
	return sh.Exec(ctx, words)
}

// Exec runs one command given as words.
func (sh *Shell) Exec(ctx context.Context, words []string) error {
	cmd, args := words[0], words[1:]
	switch cmd {
	case "help", "?":
		sh.printf("%s", helpText)
		return nil
	case "pwd":
		sh.printf("%s\n", sh.pathOf(sh.cwd))
		return nil
	case "cd":
		return sh.cd(ctx, args)
	case "ls":
		return sh.ls(ctx, args)
	case "mkdir":
		return sh.mkdir(ctx, args)
	case "rmdir":
		return sh.rmdir(ctx, args)
	case "mv":
		return sh.mv(ctx, args)
	case "cp":
		return sh.cp(ctx, args)
	case "tag":
		return sh.tag(ctx, args)
	case "rm":
		return sh.rm(ctx, args)
	case "purge":
		return sh.purge(ctx, args)
	case "restore":
		return sh.restore(ctx, args)
	case "trash":
		return sh.trash(ctx)
	case "import":
		return sh.importFiles(ctx, args)
	case "export":
		return sh.exportDocs(ctx, args)
	case "pack":
		return sh.pack(ctx, args)
	case "unpack":
		return sh.unpack(ctx, args)
	case "check":
		return sh.check(ctx)
	case "repair":
		return sh.repair(ctx)
	case "rebuild":
		return sh.rebuild(ctx)
	case "usage":
		return sh.usage(ctx)
	}
	return fmt.Errorf("console: unknown command %q (try: help)", cmd)
}

const helpText = `ls [-l] [pattern...]   cd path   pwd   mkdir path...   rmdir path...
mv source... dest[/]   cp source dest[/]   tag [+tag|-tag]... pattern...
rm pattern...   purge pattern...   restore pattern...   trash
import [-d dir] file...   export [-f format] [-o dir] [--force] pattern...
pack [--force] out.opendoc [folder]   unpack file.opendoc [dir]
check   repair   rebuild   usage
`

// Interact reads commands from the Shell's In until it ends or "exit" is typed, printing a
// prompt and each command's error. An interactive session carries on after an error; with
// Script set it stops at the first one and returns it (with the line number).
func (sh *Shell) Interact(ctx context.Context, prompt bool) error {
	r := sh.reader()
	for n := 1; ; n++ {
		if prompt {
			sh.printf("%s> ", sh.pathOf(sh.cwd))
		}
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "exit" || line == "quit" {
			return nil
		}
		if line != "" {
			if rerr := sh.Run(ctx, line); rerr != nil {
				if sh.Script {
					return fmt.Errorf("line %d (%s): %w", n, line, rerr)
				}
				sh.warnf("%v\n", rerr)
			}
		}
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (sh *Shell) reader() *bufio.Reader {
	if sh.in == nil {
		in := sh.In
		if in == nil {
			in = strings.NewReader("")
		}
		sh.in = bufio.NewReader(in)
	}
	return sh.in
}

// ask puts a question; a destructive command goes ahead only on "y".
func (sh *Shell) ask(question string) bool {
	if sh.Yes {
		return true
	}
	if sh.Script {
		// The next script line is a command, not an answer: never read it.
		sh.printf("%s\nnot confirmed: a script needs -y to answer yes\n", question)
		return false
	}
	sh.printf("%s [y/N] ", question)
	line, _ := sh.reader().ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// act does a change: with DryRun it only says what it would do.
func (sh *Shell) act(desc string, do func() error) error {
	if sh.DryRun {
		sh.printf("would %s\n", desc)
		return nil
	}
	if err := do(); err != nil {
		return fmt.Errorf("%s: %w", desc, err)
	}
	sh.printf("%s\n", desc)
	return nil
}

// ---- folders and paths ----

type tree struct {
	folders []saver.Folder
	byID    map[string]saver.Folder
}

func (sh *Shell) tree(ctx context.Context) (*tree, error) {
	fs, err := sh.Saver.Folders(ctx, sh.Target)
	if err != nil {
		return nil, err
	}
	t := &tree{folders: fs, byID: map[string]saver.Folder{}}
	for _, f := range fs {
		t.byID[f.ID] = f
	}
	return t, nil
}

func (sh *Shell) pathOf(id string) string {
	// Names come from the last tree read; a path of IDs would not be readable.
	if id == "" {
		return "/"
	}
	fs, err := sh.Saver.Folders(context.Background(), sh.Target)
	if err != nil {
		return "/?"
	}
	byID := map[string]saver.Folder{}
	for _, f := range fs {
		byID[f.ID] = f
	}
	var parts []string
	for n := 0; id != "" && n <= saver.MaxFolderDepth+1; n++ {
		f, ok := byID[id]
		if !ok {
			break
		}
		parts = append([]string{f.Name}, parts...)
		id = f.Parent
	}
	return "/" + strings.Join(parts, "/")
}

func (t *tree) child(parent, name string) (saver.Folder, error) {
	var found []saver.Folder
	for _, f := range t.folders {
		if f.Parent == parent && strings.EqualFold(f.Name, name) {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 0:
		return saver.Folder{}, fmt.Errorf("console: no folder %q here", name)
	case 1:
		return found[0], nil
	}
	return saver.Folder{}, fmt.Errorf("console: %d folders are called %q here", len(found), name)
}

// folder resolves a folder path ("/Work/Reports", "../Work", "." or "") to its ID.
func (sh *Shell) folder(t *tree, p string) (string, error) {
	cur := sh.cwd
	if strings.HasPrefix(p, "/") {
		cur = ""
	}
	for _, part := range strings.Split(p, "/") {
		switch part {
		case "", ".":
		case "..":
			cur = t.byID[cur].Parent
		default:
			f, err := t.child(cur, part)
			if err != nil {
				return "", err
			}
			cur = f.ID
		}
	}
	return cur, nil
}

// splitPath splits "Work/Reports/Q*" into the folder part and the last element.
func splitPath(p string) (dir, last string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i+1], p[i+1:]
}

// globMatch matches name against pattern, ignoring case: * is any run of characters, ? any
// one, and every other character (including [ ] and \) stands for itself.
func globMatch(pattern, name string) bool {
	return glob([]rune(strings.ToLower(pattern)), []rune(strings.ToLower(name)))
}

func glob(p, s []rune) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if glob(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
		}
		p, s = p[1:], s[1:]
	}
	return len(s) == 0
}

type thing struct {
	folder *saver.Folder
	doc    *saver.Entry
	name   string
}

// match finds the documents and folders a pattern names, and the folder they are in.
func (sh *Shell) match(ctx context.Context, t *tree, pattern string, trashed bool) (in string, out []thing, err error) {
	dir, last := splitPath(pattern)
	in, err = sh.folder(t, dir)
	if err != nil {
		return "", nil, err
	}
	var docs []saver.Entry
	if trashed {
		docs, err = sh.Saver.Trashed(ctx, sh.Target)
	} else {
		docs, err = sh.Saver.List(ctx, sh.Target)
	}
	if err != nil {
		return "", nil, err
	}
	if last == "" {
		last = "*"
	}
	for i := range docs {
		d := docs[i]
		if d.Folder == in && globMatch(last, d.Name) {
			out = append(out, thing{doc: &d, name: d.Name})
		}
	}
	if !trashed {
		for i := range t.folders {
			f := t.folders[i]
			if f.Parent == in && globMatch(last, f.Name) {
				out = append(out, thing{folder: &f, name: f.Name})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].name) < strings.ToLower(out[j].name) })
	return in, out, nil
}

func (sh *Shell) matchAll(ctx context.Context, t *tree, patterns []string, trashed bool) ([]thing, error) {
	var all []thing
	seen := map[string]bool{}
	for _, p := range patterns {
		_, got, err := sh.match(ctx, t, p, trashed)
		if err != nil {
			return nil, err
		}
		if len(got) == 0 {
			return nil, fmt.Errorf("console: nothing matches %q", p)
		}
		for _, th := range got {
			key := "f:"
			if th.folder != nil {
				key += th.folder.ID
			} else {
				key = "d:" + th.doc.ID
			}
			if !seen[key] { // overlapping patterns name a thing once
				seen[key] = true
				all = append(all, th)
			}
		}
	}
	return all, nil
}

func (th thing) label() string {
	if th.folder != nil {
		return th.name + "/"
	}
	return th.name
}

// Matches is the IDs of the documents the patterns name (folders are left out): in the trash
// when trashed is set, otherwise out of it. A host uses it to guard a command before it runs.
func (sh *Shell) Matches(ctx context.Context, trashed bool, patterns ...string) ([]string, error) {
	t, err := sh.tree(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, p := range patterns {
		_, got, err := sh.match(ctx, t, p, trashed)
		if err != nil {
			return nil, err
		}
		for _, th := range got {
			if th.doc != nil {
				ids = append(ids, th.doc.ID)
			}
		}
	}
	return ids, nil
}

func (sh *Shell) cd(ctx context.Context, args []string) error {
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	dest := ""
	if len(args) > 0 {
		dest = args[0]
	}
	if dest == "" {
		sh.cwd = ""
		return nil
	}
	id, err := sh.folder(t, dest)
	if err != nil {
		return err
	}
	sh.cwd = id
	return nil
}

func (sh *Shell) ls(ctx context.Context, args []string) error {
	long := false
	var pats []string
	for _, a := range args {
		if a == "-l" {
			long = true
		} else {
			pats = append(pats, a)
		}
	}
	if len(pats) == 0 {
		pats = []string{"*"}
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	for _, p := range pats {
		_, got, err := sh.match(ctx, t, p, false)
		if err != nil {
			return err
		}
		for _, th := range got {
			switch {
			case !long:
				sh.printf("%s\n", th.label())
			case th.folder != nil:
				sh.printf("%-8s %10s  %-16s %s\n", "folder", "", "", th.label())
			default:
				d := th.doc
				format := ""
				if d.Format != nil {
					format = d.Format.Name()
				}
				sh.printf("%-8s %10d  %-16s %s  [%s]  %s\n", format, d.Size, d.Updated.Format("2006-01-02 15:04"), th.label(), strings.Join(d.Tags, ","), d.ID)
			}
		}
	}
	return nil
}

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

// folderEmpty is the cheap check that rmdir would succeed (the trash does not count).
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
	// Into a folder: the destination names one, or ends in a slash.
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

// destIsFolder: a lone source moved to a name that is an existing folder goes into it.
func destIsFolder(t *tree, id string, things []thing) bool {
	for _, th := range things {
		if th.folder != nil && th.folder.ID == id {
			return false // a folder onto itself is not a move into it
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
		sh.printf("left alone\n")
		return nil
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

// ---- files ----

func (sh *Shell) importFiles(ctx context.Context, args []string) error {
	dest := ""
	var files []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-d" {
			if i+1 >= len(args) {
				return errors.New("console: -d needs a folder")
			}
			dest = args[i+1]
			i++
		} else {
			files = append(files, args[i])
		}
	}
	if len(files) == 0 {
		return errors.New("usage: import [-d dir] file [more]")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	folder, err := sh.folder(t, dest)
	if err != nil {
		return err
	}
	c := sh.Format
	if c == nil {
		c = docfile.JSON
	}
	for _, f := range files {
		matches, _ := filepath.Glob(f)
		if len(matches) == 0 {
			matches = []string{f}
		}
		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				return err
			}
			doc, err := sh.registry().Import("", filepath.Base(m), data)
			if err != nil {
				sh.warnf("skipped %s: %v\n", m, err)
				continue
			}
			doc.SetID("")
			doc.SetName(strings.TrimSuffix(filepath.Base(m), filepath.Ext(m)))
			doc.SetFolder(folder)
			imported := doc
			if err := sh.act("import "+m, func() error {
				_, err := sh.Saver.Save(ctx, sh.Target, c, imported)
				return err
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (sh *Shell) exportDocs(ctx context.Context, args []string) error {
	format, out, force := "open", ".", false
	var pats []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "-o":
			if i+1 >= len(args) {
				return fmt.Errorf("console: %s needs a value", args[i])
			}
			if args[i] == "-f" {
				format = args[i+1]
			} else {
				out = args[i+1]
			}
			i++
		case "--force":
			force = true
		default:
			pats = append(pats, args[i])
		}
	}
	if len(pats) == 0 {
		return errors.New("usage: export [-f format] [-o dir] [--force] pattern [more]")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	things, err := sh.matchAll(ctx, t, pats, false)
	if err != nil {
		return err
	}
	used := map[string]bool{} // file names this command has taken, lower case
	for _, th := range things {
		if th.doc == nil {
			continue
		}
		th := th
		if err := sh.act(fmt.Sprintf("export %s as %s into %s", th.name, format, out), func() error {
			doc, err := sh.Saver.Load(ctx, sh.Target, th.doc.ID)
			if err != nil {
				return err
			}
			file, data, _, err := sh.registry().Export(format, doc)
			if err != nil {
				return err
			}
			file = uniqueFile(file, th.doc.ID, used)
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			return writeFile(filepath.Join(out, file), force, func(w io.Writer) error {
				_, err := w.Write(data)
				return err
			})
		}); err != nil {
			return err
		}
	}
	return nil
}

// uniqueFile is name, or name with a short piece of the document's ID before the extension
// when another document of this command already took it.
func uniqueFile(name, id string, used map[string]bool) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	try := name
	for n := 6; used[strings.ToLower(try)]; n++ {
		if n <= len(id) {
			try = base + "-" + id[:n] + ext
		} else {
			try = fmt.Sprintf("%s-%s-%d%s", base, id, n-len(id), ext)
		}
	}
	used[strings.ToLower(try)] = true
	return try
}

// writeFile writes name through a temporary file next to it, so a failure leaves no partial
// output (and no damaged older file); an existing file is replaced only with force.
func writeFile(name string, force bool, write func(io.Writer) error) (err error) {
	if !force {
		if _, serr := os.Stat(name); serr == nil {
			return fmt.Errorf("%s exists (use --force to replace it)", name)
		}
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".docsh-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	if err = write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func (sh *Shell) pack(ctx context.Context, args []string) error {
	force := false
	var rest []string
	for _, a := range args {
		if a == "-f" || a == "--force" {
			force = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: pack [--force] out.opendoc [folder]")
	}
	if _, err := os.Stat(args[0]); err == nil && !force {
		return fmt.Errorf("%s exists (use --force to replace it)", args[0])
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	folder := ""
	if len(args) == 2 {
		if folder, err = sh.folder(t, args[1]); err != nil {
			return err
		}
	}
	return sh.act("pack "+sh.pathOf(folder)+" into "+args[0], func() error {
		var n int
		err := writeFile(args[0], force, func(w io.Writer) (err error) {
			n, err = sh.Saver.ExportPack(ctx, sh.Target, folder, w, saver.PackOptions{History: true})
			return err
		})
		if err != nil {
			return err
		}
		sh.printf("%d documents\n", n)
		return nil
	})
}

func (sh *Shell) unpack(ctx context.Context, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: unpack file.opendoc [dir]")
	}
	t, err := sh.tree(ctx)
	if err != nil {
		return err
	}
	into := ""
	if len(args) == 2 {
		if into, err = sh.folder(t, args[1]); err != nil {
			return err
		}
	}
	return sh.act("import pack "+args[0]+" into "+sh.pathOf(into), func() error {
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		c := sh.Format
		if c == nil {
			c = docfile.JSON
		}
		rep, err := sh.Saver.ImportPack(ctx, sh.Target, f, st.Size(), saver.ImportOptions{Into: into, Format: c})
		if err != nil {
			return err
		}
		sh.printf("%d documents, %d folders; %d skipped (already here), %d failed\n", rep.Documents, rep.Folders, len(rep.Skipped), len(rep.Failed))
		for _, x := range rep.Failed {
			sh.warnf("failed: %s\n", x)
		}
		return nil
	})
}

// ---- integrity ----

func (sh *Shell) check(ctx context.Context) error {
	if sh.DryRun {
		sh.printf("would read every document and compare it with its checksum\n")
		return nil
	}
	rep, err := sh.Saver.Verify(ctx, sh.Target)
	if err != nil {
		return err
	}
	sh.printf("%d documents checked: %d put back from an earlier copy, %d damaged with no copy, %d without a good previous copy, %d leftover files removed\n",
		rep.Checked, len(rep.Restored), len(rep.Lost), len(rep.BackupsLost), rep.Orphans)
	for _, n := range rep.Restored {
		sh.printf("  restored: %s\n", n)
	}
	for _, n := range rep.Lost {
		sh.printf("  LOST: %s\n", n)
	}
	if len(rep.Lost) > 0 {
		return errors.New("console: some documents are damaged and have no good copy")
	}
	return nil
}

func (sh *Shell) repair(ctx context.Context) error {
	if sh.DryRun {
		sh.printf("would compare the catalog with the files and repair what disagrees\n")
		return nil
	}
	repaired, err := sh.Saver.Repair(ctx, sh.Target)
	if err != nil {
		return err
	}
	if repaired {
		sh.printf("the catalog was repaired\n")
	} else {
		sh.printf("the catalog agrees with the files\n")
	}
	return nil
}

func (sh *Shell) rebuild(ctx context.Context) error {
	if !sh.DryRun && !sh.ask("Rebuild the catalog from the files? Folders keep their records; anything the catalog knew that is not in a file is forgotten.") {
		sh.printf("left alone\n")
		return nil
	}
	return sh.act("rebuild the catalog from the files", func() error { return sh.Saver.Rebuild(ctx, sh.Target) })
}

func (sh *Shell) usage(ctx context.Context) error {
	u, err := sh.Saver.Usage(ctx, sh.Target)
	if err != nil {
		return err
	}
	sh.printf("%d documents, %d in the trash, %d bytes used", u.Documents, u.Trashed, u.Used)
	if u.Known {
		sh.printf(" of %d (%.0f%%)", u.Total, u.Percent)
		if u.Percent >= 80 {
			sh.printf("\nWARNING: the store is filling up. Pack what you need now (pack out.opendoc), then purge old documents and versions.")
		}
	}
	sh.printf("\n")
	return nil
}
