package console

import (
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/saver"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
				if sh.Script {
					return fmt.Errorf("console: cannot import %s: %w", m, err)
				}
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
		for _, x := range rep.Warnings {
			sh.warnf("warning: %s\n", x)
		}
		switch {
		case rep.Signed && rep.SignatureOK:
			sh.printf("signed, the signature checks out (key %x)\n", rep.Signer)
		case rep.Signed:
			sh.warnf("the pack is signed but the signature does NOT check out\n")
		default:
			sh.printf("not signed\n")
		}
		if len(rep.Failed) > 0 {
			return fmt.Errorf("console: %d documents failed to import", len(rep.Failed))
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
		return sh.declined()
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
