package saver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"strings"
)

// Download hands a saved document over as a file: in the format, or as saved when
// format is "" or the session has no Export.
func (s *Session) Download(ctx context.Context, id, format string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	doc, err := s.Saver.Load(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	if format == docfile.Open.Name() {
		// Ask first whether to keep the history in the file, and say how big each part is.
		sizes, err := s.Saver.OpenSizes(ctx, s.targetName(), id)
		if err != nil {
			return err
		}
		s.set(func() { s.openDlg = &openDialog{id: id, name: doc.Name(), sizes: sizes} })
		return nil
	}
	if format == "" || s.Export == nil {
		e, err := s.Saver.Get(ctx, s.targetName(), id)
		if err != nil {
			return err
		}
		c := e.Format
		if c == nil {
			c = docfile.JSON
		}
		data, err := c.Marshal(doc)
		if err != nil {
			return err
		}
		s.Deliver(safeName(doc.Name())+c.Ext(), "application/octet-stream", data)
	} else {
		file, data, mime, err := s.Export(format, doc)
		if err != nil {
			return err
		}
		s.Deliver(file, mime, data)
	}
	s.say(msg("saver.downloaded", "%s downloaded", doc.Name()))
	return nil
}

// openDialog is the question asked before an .open download.
type openDialog struct {
	id, name string
	sizes    OpenSizes
	drop     bool
}

// DownloadOpen delivers the .open file the dialog asked about, with or without the history
// (as the person chose), and closes the dialog.
func (s *Session) DownloadOpen(ctx context.Context) error {
	s.mu.Lock()
	dlg := s.openDlg
	s.mu.Unlock()
	if dlg == nil {
		return nil
	}
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	data, err := s.Saver.ExportOpen(ctx, s.targetName(), dlg.id, !dlg.drop)
	if err != nil {
		return err
	}
	s.Deliver(safeName(dlg.name)+docfile.Open.Ext(), docfile.Open.MIME(), data)
	s.set(func() { s.openDlg = nil })
	s.say(msg("saver.downloaded", "%s downloaded", dlg.name))
	return nil
}

// DownloadPack hands over a folder ("" for all the documents) as one .opendoc pack: folders,
// tags, the trash flag and each document's history.
func (s *Session) DownloadPack(ctx context.Context, folder string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	var buf bytes.Buffer
	name := s.folderName(folder)
	// With keys (PackKeys) confidential documents are sealed into the pack; without, they are left
	// out and the person is told how many documents and folder names need a key.
	var seal func(context.Context, string, []byte) ([]byte, error)
	if s.PackKeys != nil {
		seal, _ = s.PackKeys()
	}
	folderIDs := map[string]bool{}
	s.mu.Lock()
	for _, f := range s.folders {
		folderIDs[f.ID] = true
	}
	s.mu.Unlock()
	left, leftFolders := 0, 0
	n, err := s.Saver.ExportPack(ctx, s.targetName(), folder, &buf, PackOptions{Name: name, History: true, Seal: seal, Warn: func(i PackIssue) {
		if folderIDs[i.ID] {
			leftFolders++
		} else {
			left++
		}
	}})
	if err != nil {
		return storageFull(err)
	}
	s.Deliver(safeName(name)+".opendoc", "application/vnd.opendocs.pack", buf.Bytes())
	line := msg("saver.packDownloaded", "%s downloaded as a pack of %d documents", name, n)
	if left > 0 {
		line += " " + msg("saver.packLeftOut", "(%d confidential documents left out: they need a key)", left)
	}
	if leftFolders > 0 {
		line += " " + msg("saver.packFoldersLeftOut", "(%d confidential folder names left out: they need a key)", leftFolders)
	}
	s.say(line)
	return nil
}

func (s *Session) folderName(id string) string {
	if id == "" {
		return "documents"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.folders {
		if f.ID == id {
			if f.Protection == ProtectionE2EE {
				return ConfidentialFolderName // the pack's name is in the clear: not the real one
			}
			return f.Name
		}
	}
	return "documents"
}

// countWrongKey counts the pack items whose opener said they were sealed for another key.
func countWrongKey(issues []PackIssue) int {
	n := 0
	for _, is := range issues {
		if errors.Is(is.Err, docstore.ErrWrongKey) {
			n++
		}
	}
	return n
}

// pickPack asks for a .opendoc file and imports it.
func (s *Session) pickPack() {
	s.Pick(".opendoc", func(name string, data []byte, err error) {
		s.Pending.Run(func(ctx context.Context) error {
			if err != nil {
				return err
			}
			return s.ImportPackData(ctx, name, data)
		})
	})
}

// ImportPackData imports the .opendoc pack in data into the folder being shown, keeping what is
// already there (a document with the same ID is always skipped: the UI offers no other policy), and says what came in, what was
// skipped and what failed, and whether the pack was signed.
func (s *Session) ImportPackData(ctx context.Context, name string, data []byte) error {
	s.op.Lock()
	defer s.op.Unlock()
	target, folder := s.Where()
	c, err := s.Saver.Codec(s.formatName())
	if err != nil {
		return err
	}
	var open func(context.Context, string, []byte) ([]byte, error)
	if s.PackKeys != nil {
		_, open = s.PackKeys()
	}
	rep, err := s.Saver.ImportPack(ctx, target, bytes.NewReader(data), int64(len(data)), ImportOptions{Into: folder, Format: c, Open: open})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	line := msg("saver.packImported", "%s: %d documents and %d folders imported", name, rep.Documents, rep.Folders)
	if len(rep.Skipped) > 0 {
		line += " " + msg("saver.packSkipped", "(%d already here, left as they are)", len(rep.Skipped))
	}
	if wrong := countWrongKey(rep.NeedsKey); wrong > 0 {
		// Keys are unlocked here and none opens them: unlocking the store again would not help.
		line += " " + msg("saver.packWrongKey", "(%d confidential items were sealed for another key: this pack was sealed for another key than the ones unlocked here)", wrong)
	}
	if need := len(rep.NeedsKey) - countWrongKey(rep.NeedsKey); need > 0 {
		line += " " + msg("saver.packNeedsKey", "(%d confidential items need a key: unlock the store and import the pack again)", need)
	}
	if len(rep.Warnings) > 0 {
		line += " " + msg("saver.packWarnings", "(%d warnings: %s)", len(rep.Warnings), strings.Join(rep.Warnings[:min(3, len(rep.Warnings))], "; "))
	}
	if len(rep.Failed) > 0 {
		line += " " + msg("saver.packFailed", "(%d failed: %s)", len(rep.Failed), strings.Join(rep.Failed[:min(3, len(rep.Failed))], "; "))
	}
	if rep.Signed {
		if rep.SignatureOK {
			line += " " + msg("saver.packSigned", "(signed by key %x)", rep.Signer[:min(8, len(rep.Signer))])
		} else {
			line += " " + msg("saver.packBadSignature", "(WARNING: the signature does not match the pack)")
		}
	}
	return s.after(ctx, line)
}

func (s *Session) formatName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.format
}

// CancelOpenDownload closes the dialog without downloading.
func (s *Session) CancelOpenDownload() { s.set(func() { s.openDlg = nil }) }

// DownloadFolder hands over a ZIP of a folder's documents ("" for all of them).
func (s *Session) DownloadFolder(ctx context.Context, id string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	data, name, skipped, err := s.Saver.ArchiveReport(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	s.Deliver(safeName(name)+".zip", "application/zip", data)
	if len(skipped) > 0 {
		s.say(msg("saver.downloadedSkipped", "%s downloaded without %d confidential document(s) that are locked here", name, len(skipped)))
		return nil
	}
	s.say(msg("saver.downloaded", "%s downloaded", name))
	return nil
}

// CopyLink copies the link to a document or folder and says so.
func (s *Session) CopyLink(url string) {
	if s.Copy != nil {
		s.Copy(url)
		s.say(msg("saver.linkCopied", "Link copied: %s", url))
		return
	}
	s.say(url)
}

func (s *Session) downloadChoices() []components.DocChoice {
	var out []components.DocChoice
	if s.Export != nil {
		for _, c := range s.DownloadFormats {
			out = append(out, components.DocChoice(c))
		}
	}
	return out
}

// Where is the target and folder a new document would be saved in.
func (s *Session) Where() (target, folder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target, s.folder
}
