package docstorecmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/docstore"
)

// runOperatorCommand handles `docstore backup <file>` and `docstore restore [-force] <file>`, which
// work on the store the environment configures and then exit. It reports whether args was one of
// them; code is the exit status when it was. The backup holds exactly what the store holds (the
// ciphertext of an encrypted store), so no key is needed and none is read. Each command writes one
// audit event (package audit) to stderr, without any name or id in it.
func runOperatorCommand(ctx context.Context, adapter docstore.Adapter, args []string, stdout, stderr io.Writer) (handled bool, code int) {
	if len(args) == 0 || (args[0] != "backup" && args[0] != "restore") {
		return false, 0
	}
	log := audit.NewLogger(stderr)
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	force := fs.Bool("force", false, "restore into a store that already has documents (documents with the same id are replaced)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 || (*force && args[0] == "backup") {
		_, _ = fmt.Fprintf(stderr, "usage: docstore backup <file>\n       docstore restore [-force] <file>\n")
		return true, 2
	}
	file := fs.Arg(0)
	switch args[0] {
	case "backup":
		n, err := backupToFile(ctx, adapter, file)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "docstore: backup failed: %v\n", err)
			_ = log.Log(audit.Event{Kind: audit.BackupMade, Outcome: audit.Failed, Actor: audit.Operator, Reason: audit.Unavailable})
			return true, 1
		}
		_, _ = fmt.Fprintf(stdout, "backed up %d documents to %s\n", n, file)
		_ = log.Log(audit.Event{Kind: audit.BackupMade, Outcome: audit.OK, Actor: audit.Operator, Count: n})
	case "restore":
		n, err := restoreFromFile(ctx, adapter, file, *force)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "docstore: restore failed: %v\n", err)
			reason := audit.Unavailable
			if errors.Is(err, docstore.ErrBadBackup) {
				reason = audit.BadSignature
			} else if errors.Is(err, errNotEmpty) {
				reason = audit.Conflict
			}
			_ = log.Log(audit.Event{Kind: audit.BackupRestored, Outcome: audit.Failed, Actor: audit.Operator, Reason: reason})
			return true, 1
		}
		_, _ = fmt.Fprintf(stdout, "restored %d documents from %s\n", n, file)
		_ = log.Log(audit.Event{Kind: audit.BackupRestored, Outcome: audit.OK, Actor: audit.Operator, Count: n})
	}
	return true, 0
}

var errNotEmpty = errors.New("the store already has documents; use -force to restore into it (same ids are replaced)")

// backupToFile writes the backup next to file and renames it into place, so a failed run never
// leaves a half file under the final name. The file is readable by its owner only.
func backupToFile(ctx context.Context, adapter docstore.Adapter, file string) (int, error) {
	tmp, err := os.CreateTemp(filepath.Dir(file), ".backup-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; cleans up after a failure
	man, err := docstore.Backup(ctx, adapter, tmp)
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	return len(man.Entries), os.Rename(tmp.Name(), file)
}

func restoreFromFile(ctx context.Context, adapter docstore.Adapter, file string, force bool) (int, error) {
	f, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	if !force {
		metas, err := adapter.List(ctx)
		if err != nil {
			return 0, err
		}
		if len(metas) > 0 {
			return 0, errNotEmpty
		}
	}
	return docstore.Restore(ctx, adapter, f)
}
