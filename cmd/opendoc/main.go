package main

import (
	"bytes"
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
	"unicode"
	"unicode/utf8"

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
		return errors.New("usage: opendoc pack|convert|unpack|list|verify|sign|cosign|recover|keygen ... (see the command's doc)")
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
	case "cosign":
		return cosignCmd(args, out)
	case "recover":
		return recoverCmd(args, out)
	case "keygen":
		return keygenCmd(args, out)
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

func keygenCmd(args []string, out outWriter) error {
	if len(args) > 1 {
		return errors.New("usage: opendoc keygen [key-file]")
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("no random bytes: %w", err)
	}
	if len(args) == 0 {
		say(out, "%s\n", hex.EncodeToString(seed))
		return nil
	}
	f, err := os.OpenFile(args[0], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(hex.EncodeToString(seed) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(args[0])
		return err
	}
	pub, _ := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	say(out, "%s written; public key %x\n", args[0], []byte(pub))
	return nil
}

func esc(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029 {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

type output struct {
	f   *os.File
	dst string
}

func newOutput(dst string) (*output, error) {
	f, err := os.CreateTemp(filepath.Dir(dst), ".opendoc-*.tmp")
	if err != nil {
		return nil, err
	}
	return &output{f: f, dst: dst}, nil
}

func (o *output) finish(err error) error {
	if err == nil {
		err = o.f.Sync()
	}
	if cerr := o.f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		if err = os.Chmod(o.f.Name(), 0o644); err == nil {
			err = os.Rename(o.f.Name(), o.dst)
		}
	}
	if err != nil {
		_ = os.Remove(o.f.Name())
	}
	return err
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	return err == nil && os.SameFile(ia, ib)
}

func registry() *transfer.Registry {
	r := transfer.Default()
	export.Register(r)
	return r
}

type outWriter interface{ Write([]byte) (int, error) }

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
	o, err := newOutput(dst)
	if err != nil {
		return err
	}
	skip := []string{o.f.Name(), dst}
	done := false
	defer func() {
		if !done {
			_ = o.finish(errors.New("failed"))
		}
	}()
	f := o.f
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
		added, bad, err := addTree(w, reg, in, skip)
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
	done = true
	if err := o.finish(nil); err != nil {
		return err
	}
	say(out, "%s: %d documents", dst, n)
	if skipped > 0 {
		say(out, ", %d skipped", skipped)
	}
	say(out, "\n")
	return nil
}

func addTree(w *pack.Writer, reg *transfer.Registry, root string, skip []string) (added int, skipped []string, err error) {
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
		for _, s := range skip {
			if sameFile(p, s) {
				return nil
			}
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
	fl := flag.NewFlagSet("unpack", flag.ContinueOnError)
	force := fl.Bool("force", false, "overwrite files that already exist")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() != 2 {
		return errors.New("usage: opendoc unpack [--force] pack.opendoc dir")
	}
	f, r, err := openPack(fl.Arg(0))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	outDir := fl.Arg(1)
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
	written, failed := 0, 0
	fail := func(e pack.Entry, err error) {
		failed++
		fmt.Fprintf(os.Stderr, "opendoc: %s (%s): %v\n", esc(e.Name), esc(e.ID), err)
	}
	for _, e := range idx.Entries {
		if e.Encrypted {
			fail(e, errors.New("encrypted: this tool has no key, so it is not unpacked"))
			continue
		}
		data, err := r.ReadRaw(e.ID)
		if err != nil {
			fail(e, err)
			continue
		}
		dir := filepath.Join(outDir, dirOf(e.Folder))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(e, err)
			continue
		}
		ext := docfile.Open.Ext()
		name := safe(e.Name)
		path := filepath.Join(dir, name+ext)
		if used[strings.ToLower(path)] {
			suffix := " (" + shortID(e.ID) + ")"
			path = filepath.Join(dir, truncate(name, maxName-len(suffix))+suffix+ext)
		}
		used[strings.ToLower(path)] = true
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if !*force {
			flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
		}
		w, err := os.OpenFile(path, flags, 0o644)
		if errors.Is(err, fs.ErrExist) {
			fail(e, fmt.Errorf("%s exists (use --force to overwrite)", path))
			continue
		}
		if err != nil {
			fail(e, err)
			continue
		}
		_, err = w.Write(data)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(path)
			fail(e, err)
			continue
		}
		written++
	}
	say(out, "%d documents written to %s\n", written, outDir)
	if failed > 0 {
		return fmt.Errorf("%d of %d documents were not written", failed, len(idx.Entries))
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return safe(id[:8])
	}
	return safe(id)
}

func safe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ".")
	s = strings.Trim(truncate(s, maxName), ". ")
	if s == "" {
		s = "document"
	}
	return s
}

const maxName = 240

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
	say(out, "%s (%s), %d documents, %d folders", esc(m.Name), esc(m.Producer), len(idx.Entries), len(idx.Folders))
	if signers := r.Signers(); len(signers) > 0 {
		say(out, ", signed by %s (the keys are not checked against anyone)", signersText(signers))
	}
	say(out, "\n")
	names := map[string]string{}
	for _, fo := range idx.Folders {
		names[fo.ID] = fo.Name
	}
	es := append([]pack.Entry(nil), idx.Entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	for _, e := range es {
		folder := esc(names[e.Folder])
		if e.Trashed {
			folder += " (trash)"
		}
		if e.Encrypted {
			folder += " (encrypted)"
		}
		tags := make([]string, len(e.Tags))
		for i, t := range e.Tags {
			tags[i] = esc(t)
		}
		say(out, "%s  %8d  %-30s %s %s\n", esc(e.ID), e.Length, esc(e.Name), folder, strings.Join(tags, ","))
	}
	return nil
}

func verifyCmd(args []string, out outWriter) error {
	fl := flag.NewFlagSet("verify", flag.ContinueOnError)
	pubHex := fl.String("pubkey", "", "require the pack to be signed by this Ed25519 public key (64 hex characters)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() != 1 {
		return errors.New("usage: opendoc verify [-pubkey hex] pack.opendoc")
	}
	var want []byte
	if *pubHex != "" {
		var err error
		if want, err = hex.DecodeString(strings.TrimSpace(*pubHex)); err != nil || len(want) != ed25519.PublicKeySize {
			return errors.New("-pubkey is an Ed25519 public key as 64 hex characters")
		}
	}
	f, r, err := openPack(fl.Arg(0))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	rep := r.Verify()
	say(out, "%d documents checked", rep.Checked)
	if n := len(r.Index().Attachments); n > 0 {
		say(out, ", %d attachments", n)
	}
	if rep.Unindexed > 0 {
		say(out, ", %d records not in the index", rep.Unindexed)
	}
	if rep.Signed {
		say(out, ", signed by %s", signersText(rep.Signers))
		if want == nil {
			say(out, " (not checked against anyone: use -pubkey)")
		}
	}
	say(out, "\n")
	for _, p := range rep.Bad {
		say(out, "BAD %s (%s): %v\n", esc(p.Name), esc(p.ID), p.Err)
	}
	if rep.Gap {
		say(out, "%s\n", "BAD the records do not follow one another up to the index")
	}
	signedByWant := false
	for _, sg := range rep.Signers {
		signedByWant = signedByWant || sg.Valid && bytes.Equal(sg.PublicKey, want)
	}
	if want != nil && !signedByWant {
		say(out, "BAD the pack is not signed by the key given\n")
		return errors.New("the pack is not signed by the key given")
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

func cosignCmd(args []string, out outWriter) error {
	fl := flag.NewFlagSet("cosign", flag.ContinueOnError)
	keyPath := fl.String("key", "", "the key file")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() != 1 || *keyPath == "" {
		return errors.New("usage: opendoc cosign -key key pack.opendoc")
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
	if err := pack.Cosign(f, st.Size(), key); err != nil {
		return err
	}
	say(out, "%s co-signed\n", fl.Arg(0))
	return nil
}

func signersText(signers []pack.Signer) string {
	var parts []string
	for _, s := range signers {
		parts = append(parts, fmt.Sprintf("%x (valid: %v)", s.PublicKey[:min(8, len(s.PublicKey))], s.Valid))
	}
	return strings.Join(parts, ", ")
}

func recoverCmd(args []string, out outWriter) error {
	if len(args) != 2 {
		return errors.New("usage: opendoc recover pack.opendoc out.opendoc")
	}
	if sameFile(args[0], args[1]) {
		return errors.New("recover writes a new pack: the output must not be the pack being read")
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
	o, err := newOutput(args[1])
	if err != nil {
		return err
	}
	if err := o.finish(pack.Repack(rec, o.f)); err != nil {
		return err
	}
	say(out, "%d documents recovered into %s (complete: %v)\n", len(rec.Docs), args[1], rec.Complete)
	if rec.SealedLost > 0 {
		say(out, "WARNING: %d encrypted (confidential) record(s) could not be attributed because the index is damaged, and are NOT in %s. "+
			"Recover from a copy of the pack whose index is intact first, if you have one.\n", rec.SealedLost, args[1])
	}
	return nil
}
