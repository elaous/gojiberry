package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
	"io"
	"sort"
	"strings"
	"unicode"
)

var registerExport func(*transfer.Registry)

// Shell runs console commands on one target of a Saver.
type Shell struct {
	Saver  *saver.Saver
	Target string
	// In answers questions; Out and Err get the output.
	In       io.Reader
	Out, Err io.Writer
	// DryRun says what would change and changes nothing.
	DryRun, Yes bool
	// Script says the commands come from a file.
	Script bool
	// Registry reads and writes import and export files.
	Registry *transfer.Registry
	Files    Files
	Confirm  func(question string) bool
	// Format is how imported documents are stored.
	Format docfile.Codec

	cwd string // the folder ID being worked in, "" for the top
	in  *bufio.Reader
}

// New makes a Shell on target.
func New(s *saver.Saver, target string, in io.Reader, out, errw io.Writer) *Shell {
	return &Shell{Saver: s, Target: target, In: in, Out: out, Err: errw}
}

// Cwd is the ID of the folder commands work in.
func (sh *Shell) Cwd() string { return sh.cwd }

// SetCwd sets the folder commands work in.
func (sh *Shell) SetCwd(id string) { sh.cwd = id }

func (sh *Shell) registry() *transfer.Registry {
	if sh.Registry == nil {
		r := transfer.Default()
		if registerExport != nil {
			registerExport(r)
		}
		sh.Registry = r
	}
	return sh.Registry
}

func (sh *Shell) printf(format string, a ...any) { _, _ = fmt.Fprintf(sh.Out, format, a...) }

func (sh *Shell) warnf(format string, a ...any) { _, _ = fmt.Fprintf(sh.Err, format, a...) }

// Split breaks a command line into words; spaces separate them.
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

// Interact reads commands from In until the input ends.
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

func (sh *Shell) ask(question string) bool {
	if sh.Yes {
		return true
	}
	if sh.Script {
		sh.printf("%s\nnot confirmed: a script needs -y to answer yes\n", question)
		return false
	}
	if sh.Confirm != nil {
		return sh.Confirm(question)
	}
	sh.printf("%s [y/N] ", question)
	line, _ := sh.reader().ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func (sh *Shell) declined() error {
	if sh.Script {
		return errors.New("not confirmed: nothing was changed (a script needs -y to answer yes)")
	}
	sh.printf("left alone\n")
	return nil
}

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

func splitPath(p string) (dir, last string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i+1], p[i+1:]
}

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

// Matches is the IDs of the documents the patterns name.
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
