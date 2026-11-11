package docstorecmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/docstore"
)

const defaultKeep = 7

// matches only files backup writes: rotation never touches others
var backupName = regexp.MustCompile(`^gojiberry-backup-\d{8}T\d{6}Z\.tar$`)

func scheduledName(t time.Time) string {
	return "gojiberry-backup-" + t.UTC().Format("20060102T150405Z") + ".tar"
}

// only after a new backup was written: a failed run keeps old ones
func rotateBackups(dir string, keep int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && backupName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // oldest first
	removed := 0
	for len(names)-removed > keep {
		if err := os.Remove(filepath.Join(dir, names[removed])); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func runScheduledBackups(ctx context.Context, adapter docstore.Adapter, dir string, keep int, every time.Duration, stdout, stderr io.Writer, log *audit.Logger) int {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		_, _ = fmt.Fprintf(stderr, "docstore: backup failed: %v\n", err)
		return 1
	}
	failed := false
	round := func() {
		file := filepath.Join(dir, scheduledName(time.Now()))
		n, err := backupToFile(ctx, adapter, file)
		if err != nil {
			failed = true
			_, _ = fmt.Fprintf(stderr, "docstore: backup failed: %v\n", err)
			_ = log.Log(audit.Event{Kind: audit.BackupMade, Outcome: audit.Failed, Actor: audit.System, Reason: audit.Unavailable})
			return
		}
		failed = false
		_, _ = fmt.Fprintf(stdout, "backed up %d documents to %s\n", n, file)
		_ = log.Log(audit.Event{Kind: audit.BackupMade, Outcome: audit.OK, Actor: audit.System, Count: n})
		if removed, err := rotateBackups(dir, keep); err != nil {
			_, _ = fmt.Fprintf(stderr, "docstore: removing old backups failed: %v\n", err)
		} else if removed > 0 {
			_, _ = fmt.Fprintf(stdout, "removed %d old backups, keeping %d\n", removed, keep)
		}
	}
	round()
	if every <= 0 {
		if failed {
			return 1
		}
		return 0
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-t.C:
			round()
		}
	}
}
