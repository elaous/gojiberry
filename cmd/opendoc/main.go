// Command opendoc works with .opendoc packs: many documents and their folders in one file
// (docs/opendoc-format.md).
//
//	opendoc pack    [-sign key] [-name N] out.opendoc  file-or-folder...
//	opendoc convert [-sign key] [-name N] out.opendoc  file-or-folder...   (the same: .docx, .pdf, .md, .txt, .open, .json too)
//	opendoc unpack  pack.opendoc  dir     (each document as a .open file, folders as directories)
//	opendoc list    pack.opendoc
//	opendoc verify  pack.opendoc          (reads every document; exit 1 if anything is wrong)
//	opendoc sign    -key key pack.opendoc (signs, or signs again, in place)
//	opendoc recover pack.opendoc out.opendoc  (what is intact in a cut or damaged pack)
//
// A key is a file holding an Ed25519 seed as 64 hex characters (opendoc keygen prints one).
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "opendoc:", err)
		os.Exit(1)
	}
}

func run(args []string, out outWriter) error {
	if len(args) == 0 {
		return errors.New("usage: opendoc pack|convert|unpack|list|verify|sign|recover|keygen ... (see the command's doc)")
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "pack", "convert":
		return packCmd(args, out)
	case "unpack":
		return unpackCmd(args, out)
	case "list":
		return listCmd(args, out)
	case "verify":
		return verifyCmd(args, out)
	case "sign":
		return signCmd(args, out)
	case "recover":
		return recoverCmd(args, out)
	case "keygen":
		seed := make([]byte, ed25519.SeedSize)
		_, _ = rand.Read(seed)
		say(out, "%s\n", hex.EncodeToString(seed))
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func readKey(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("a key file holds an Ed25519 seed as 64 hex characters")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func registry() *transfer.Registry {
	r := transfer.Default()
	export.Register(r)
	return r
}

type outWriter interface{ Write([]byte) (int, error) }

// say writes to the command's output; a failing stdout is not something to handle.
func say(out outWriter, format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }

func packCmd(args []string, out outWriter) error {
	fl := flag.NewFlagSet("pack", flag.ContinueOnError)
	keyPath := fl.String("sign", "", "sign the pack with this key file")
	name := fl.String("name", "", "the pack's name")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() < 2 {
		return errors.New("usage: opendoc pack [-sign key] [-name N] out.opendoc files-or-folders")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	dst, inputs := fl.Arg(0), fl.Args()[1:]
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	pname := *name
	if pname == "" {
		pname = strings.TrimSuffix(filepath.Base(dst), filepath.Ext(dst))
	}
	w, err := pack.NewWriter(f, pack.Meta{ID: docfile.NewID(), Name: pname, Created: time.Now().UnixNano(), Producer: "opendoc"})
	if err != nil {
		return err
	}
	reg := registry()
	n, skipped := 0, 0
	for _, in := range inputs {
		added, bad, err := addTree(w, reg, in)
		if err != nil {
			return err
		}
		n, skipped = n+added, skipped+len(bad)
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "opendoc: skipped", b)
		}
	}
	if err := w.Close(key); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	say(out, "%s: %d documents", dst, n)
	if skipped > 0 {
		say(out, ", %d skipped", skipped)
	}
	say(out, "\n")
	return nil
}

// addTree adds a file, or the files of a folder (its subfolders becoming folders of the
// pack), converting each to a document.
func addTree(w *pack.Writer, reg *transfer.Registry, root string) (added int, skipped []string, err error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, nil, err
	}
	folders := map[string]string{} // directory -> folder ID
	base := filepath.Dir(root)
	if info.IsDir() {
		base = root
	}
	var ensure func(dir string) (string, error)
	ensure = func(dir string) (string, error) {
		if dir == base || dir == "." || dir == "" {
			return "", nil
		}
		if id, ok := folders[dir]; ok {
			return id, nil
		}
		parent, err := ensure(filepath.Dir(dir))
		if err != nil {
			return "", err
		}
		id := docfile.NewID()
		if err := w.AddFolder(pack.Folder{ID: id, Name: filepath.Base(dir), Parent: parent}); err != nil {
			return "", err
		}
		folders[dir] = id
		return id, nil
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		doc, err := reg.Import("", filepath.Base(p), data)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", p, err))
			return nil
		}
		if !docfile.ValidID(doc.ID()) {
			doc.SetID(docfile.NewID())
		}
		if doc.Name() == "" {
			doc.SetName(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)))
		}
		if doc.Updated == 0 {
			if fi, err := d.Info(); err == nil {
				doc.Updated = fi.ModTime().UnixNano()
			}
		}
		folder, err := ensure(filepath.Dir(p))
		if err != nil {
			return err
		}
		doc.SetFolder(folder)
		if err := w.Add(doc); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", p, err))
			return nil
		}
		added++
		return nil
	})
	return added, skipped, err
}

func openPack(path string) (*os.File, *pack.Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	r, err := pack.Open(f, st.Size())
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s: %w (try: opendoc recover)", path, err)
	}
	return f, r, nil
}

func unpackCmd(args []string, out outWriter) error {
	if len(args) != 2 {
		return errors.New("usage: opendoc unpack pack.opendoc dir")
	}
	f, r, err := openPack(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	idx := r.Index()
	folders := map[string]pack.Folder{}
	for _, fo := range idx.Folders {
		folders[fo.ID] = fo
	}
	dirOf := func(id string) string {
		var parts []string
		for depth := 0; id != "" && depth <= pack.MaxDepth; depth++ {
			fo, ok := folders[id]
			if !ok {
				break
			}
			parts = append([]string{safe(fo.Name)}, parts...)
			id = fo.Parent
		}
		return filepath.Join(parts...)
	}
	used := map[string]bool{}
	for _, e := range idx.Entries {
		data, err := r.ReadRaw(e.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "opendoc: %s (%s): %v\n", e.Name, e.ID, err)
			continue
		}
		dir := filepath.Join(args[1], dirOf(e.Folder))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		name := safe(e.Name)
		path := filepath.Join(dir, name+docfile.Open.Ext())
		if used[strings.ToLower(path)] {
			path = filepath.Join(dir, name+" ("+e.ID[:8]+")"+docfile.Open.Ext())
		}
		used[strings.ToLower(path)] = true
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	say(out, "%d documents written to %s\n", len(idx.Entries), args[1])
	return nil
}

func safe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ".")
	if s == "" {
		s = "document"
	}
	return s
}

func listCmd(args []string, out outWriter) error {
	if len(args) != 1 {
		return errors.New("usage: opendoc list pack.opendoc")
	}
	f, r, err := openPack(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	m, idx := r.Meta(), r.Index()
	say(out, "%s (%s), %d documents, %d folders", m.Name, m.Producer, len(idx.Entries), len(idx.Folders))
	if sig, ok := r.Signature(); sig != nil {
		say(out, ", signed by %x (valid: %v)", sig.PublicKey[:min(8, len(sig.PublicKey))], ok)
	}
	say(out, "\n")
	names := map[string]string{}
	for _, fo := range idx.Folders {
		names[fo.ID] = fo.Name
	}
	es := append([]pack.Entry(nil), idx.Entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	for _, e := range es {
		folder := names[e.Folder]
		if e.Trashed {
			folder += " (trash)"
		}
		say(out, "%s  %8d  %-30s %s %s\n", e.ID, e.Length, e.Name, folder, strings.Join(e.Tags, ","))
	}
	return nil
}

func verifyCmd(args []string, out outWriter) error {
	if len(args) != 1 {
		return errors.New("usage: opendoc verify pack.opendoc")
	}
	f, r, err := openPack(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	rep := r.Verify()
	say(out, "%d documents checked", rep.Checked)
	if rep.Unindexed > 0 {
		say(out, ", %d records not in the index", rep.Unindexed)
	}
	if rep.Signed {
		say(out, ", signed by %x (valid: %v)", rep.Signer[:min(8, len(rep.Signer))], rep.SignatureOK)
	}
	say(out, "\n")
	for _, p := range rep.Bad {
		say(out, "BAD %s (%s): %v\n", p.Name, p.ID, p.Err)
	}
	if rep.Gap {
		say(out, "%s\n", "BAD the records do not follow one another up to the index")
	}
	if !rep.OK() {
		return errors.New("the pack has problems")
	}
	return nil
}

func signCmd(args []string, out outWriter) error {
	fl := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyPath := fl.String("key", "", "the key file")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() != 1 || *keyPath == "" {
		return errors.New("usage: opendoc sign -key key pack.opendoc")
	}
	key, err := readKey(*keyPath)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(fl.Arg(0), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	a, err := pack.OpenAppend(f, st.Size(), key)
	if err != nil {
		return err
	}
	if err := a.Close(); err != nil {
		return err
	}
	say(out, "%s signed\n", fl.Arg(0))
	return nil
}

func recoverCmd(args []string, out outWriter) error {
	if len(args) != 2 {
		return errors.New("usage: opendoc recover pack.opendoc out.opendoc")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	rec, err := pack.Recover(f, st.Size())
	if err != nil {
		return err
	}
	dst, err := os.Create(args[1])
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if err := pack.Repack(rec, dst); err != nil {
		return err
	}
	say(out, "%d documents recovered into %s (complete: %v)\n", len(rec.Docs), args[1], rec.Complete)
	return nil
}
