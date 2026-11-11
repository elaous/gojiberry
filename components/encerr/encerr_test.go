package encerr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/i18n"
)

func TestExplainErrorGroupsEverySentinel(t *testing.T) {
	Install()
	Install() // twice is harmless
	cases := []struct {
		name string
		err  error
		want components.ErrorKind
	}{
		{"unsigned after flip (wraps forged)", fmt.Errorf("%w: %w: doc1", encrypt.ErrUnsignedAfterFlip, encrypt.ErrForgedFrame), components.ErrKindUnsigned},
		{"forged frame", fmt.Errorf("%w: doc1: bad signature", encrypt.ErrForgedFrame), components.ErrKindForged},
		{"wrong document", encrypt.ErrWrongDocument, components.ErrKindForged},
		{"no frame signer", fmt.Errorf("%w: doc1", encrypt.ErrNoFrameSigner), components.ErrKindNoSigner},
		{"downgrade", fmt.Errorf("x: %w", encrypt.ErrDowngrade), components.ErrKindRolledBack},
		{"stale record", fmt.Errorf("doc: %w", encrypt.ErrStaleRecord), components.ErrKindRolledBack},
		{"unreadable", fmt.Errorf("%w: doc1: %w", encrypt.ErrUnreadable, errors.New("age: bad header")), components.ErrKindUnreadable},
		{"no key (also locked)", encrypt.ErrNoKey, components.ErrKindNoKey},
		{"locked", docstore.ErrLocked, components.ErrKindLocked},
		{"floor degraded", fmt.Errorf("%w: %w", encrypt.ErrFloorDegraded, errors.New("quota")), components.ErrKindFloor},
		{"passkey alone", encrypt.ErrNeedSecondRecipient, components.ErrKindRecovery},
		{"conflict", docstore.ErrConflict, components.ErrKindConflict},
		{"storage full", docstore.ErrStorageFull, components.ErrKindStorageFull},
		{"offline", context.DeadlineExceeded, components.ErrKindOffline},
		{"unknown", errors.New("boom"), components.ErrKindUnknown},
	}
	banned := []string{"Err", "Repair", "AcceptUnsigned", "Sharing", "Recover(", "encrypt:", "docstore:", "Signer", "Adapter"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			info := components.ExplainError(c.err)
			if info.Kind != c.want {
				t.Fatalf("kind %q, want %q", info.Kind, c.want)
			}
			for _, b := range banned {
				if strings.Contains(info.Message, b) {
					t.Errorf("message names %q: %s", b, info.Message)
				}
			}
			if info.Message == info.Details || info.Details == "" {
				t.Errorf("message %q details %q", info.Message, info.Details)
			}
		})
	}
}

func TestEveryMessageIsInEveryPack(t *testing.T) {
	ids := []string{"err.details", "err.copy", "err.rolledBack", "err.locked", "err.conflict", "err.storageFull", "err.offline", "err.unknown"}
	for _, g := range Groups() {
		if g.ID != "" {
			ids = append(ids, g.ID)
		}
	}
	cat := i18n.Default()
	for _, id := range ids {
		for _, l := range []string{"de", "fr", "es"} {
			if got := cat.Text(l, id, "MISSING"); got == "MISSING" {
				t.Errorf("%s has no %q", l, id)
			}
		}
	}
}
