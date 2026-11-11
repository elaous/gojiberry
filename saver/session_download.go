package saver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
	"github.com/elaous/gojiberry/docstore"
	"strings"
)

func (s *Session) Download(ctx context.Context, id, format string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	doc, err := s.Saver.Load(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	if format == docfile.Open.Name() {
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

type openDialog struct {
	id, name string
	sizes    OpenSizes
	drop     bool
}

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

func (s *Session) DownloadPack(ctx context.Context, folder string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	var buf bytes.Buffer
	name := s.folderName(folder)
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
	n, err := s.Saver.ExportPack(ctx, s.targetName(), folder, &buf, PackOptions{Name: name, History: true, Seal: seal, Blur: true, Attachments: DefaultAttachmentSize, Warn: func(i PackIssue) {
		if folderIDs[i.ID] {
			leftFolders++
		} else {
			left++
		}
	}})
	if err != nil {
		return storageFull(err)
	}
	s.Deliver(safeName(name)+".opendoc", pack.MIME, buf.Bytes())
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
				return ConfidentialFolderName
			}
			return f.Name
		}
	}
	return "documents"
}

func countWrongKey(issues []PackIssue) int {
	n := 0
	for _, is := range issues {
		if errors.Is(is.Err, docstore.ErrWrongKey) {
			n++
		}
	}
	return n
}

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

func (s *Session) Where() (target, folder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target, s.folder
}
