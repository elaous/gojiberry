package components

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/xango"
)

// ErrorKind says in which user-facing group an error falls.
type ErrorKind string

// The groups ExplainError sorts errors into.
const (
	ErrKindApp        ErrorKind = "app"
	ErrKindUnsigned   ErrorKind = "unsigned"
	ErrKindForged     ErrorKind = "forged"
	ErrKindNoSigner   ErrorKind = "nosigner"
	ErrKindRolledBack ErrorKind = "rolledback"
	// ErrKindUnreadable is a stored file that cannot be opened here.
	ErrKindUnreadable ErrorKind = "unreadable"
	ErrKindNoKey      ErrorKind = "nokey"
	// ErrKindLocked is a store or document that is locked.
	ErrKindLocked ErrorKind = "locked"
	// ErrKindConflict is a document changed elsewhere since it was read.
	ErrKindConflict ErrorKind = "conflict"
	// ErrKindFloor is the browser refusing to keep the safety record.
	ErrKindFloor ErrorKind = "floor"
	// ErrKindStorageFull is a store with no room left.
	ErrKindStorageFull ErrorKind = "storagefull"
	ErrKindRecovery    ErrorKind = "recovery"
	ErrKindPasskey     ErrorKind = "passkey"
	// ErrKindOffline is a request that did not reach its server.
	ErrKindOffline ErrorKind = "offline"
	// ErrKindUnknown is everything else.
	ErrKindUnknown ErrorKind = "unknown"
)

type UserMessager interface{ UserMessage() string }

// ErrorInfo is an error as a person reads it.
type ErrorInfo struct {
	Kind ErrorKind
	// Message is plain, translated and says what to do next.
	Message string
	// Details is the technical text, for support.
	Details string
}

// CopyText puts text on the clipboard.
var CopyText func(text string)

type ErrorGroup struct {
	Kind        ErrorKind
	ID, Default string
	// Match says whether err belongs to the group.
	Match func(err error) bool
	As    error
}

var errorGroups []ErrorGroup

// call once before first render; most specific group first
func RegisterErrorGroups(g ...ErrorGroup) { errorGroups = append(errorGroups, g...) }

// ExplainError sorts err into a group with a translated message.
func ExplainError(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}
	details := err.Error()
	var um UserMessager
	if errors.As(err, &um) {
		if inner := errors.Unwrap(err); inner != nil {
			details = inner.Error()
		}
		return ErrorInfo{Kind: ErrKindApp, Message: um.UserMessage(), Details: details}
	}
	kind, id, def := classifyError(err)
	return ErrorInfo{Kind: kind, Message: tr(id, def), Details: details}
}

func classifyError(err error) (ErrorKind, string, string) {
	for _, g := range errorGroups {
		if g.Match(err) {
			if g.As != nil {
				return classifyError(g.As)
			}
			return g.Kind, g.ID, g.Default
		}
	}
	switch {
	case errors.Is(err, docstore.ErrDowngrade):
		return ErrKindRolledBack, "err.rolledBack", "This document or its list of people is in an older state than this browser has seen before, so it was not used. A newer version may have been deleted or an old copy put back. If you did not do that, do not trust it; the owner can restore it with a recovery key."
	case errors.Is(err, docstore.ErrLocked):
		return ErrKindLocked, "err.locked", "The store is locked. Unlock it with one of its keys first."
	case errors.Is(err, docstore.ErrConflict):
		return ErrKindConflict, "err.conflict", "This document was changed somewhere else (another tab, window or person) since you opened it. Nothing was overwritten: reload it and make your change again."
	case errors.Is(err, docstore.ErrStorageFull):
		return ErrKindStorageFull, "err.storageFull", "The browser's storage is full. Nothing was changed. Download your documents and delete what you do not need, then try again."
	case isOffline(err):
		return ErrKindOffline, "err.offline", "The server could not be reached. Check your connection and try again; your work on this device is not lost."
	}
	return ErrKindUnknown, "err.unknown", "Something went wrong and nothing you can fix from here was found. Try again or reload the page. If it keeps happening, copy the details and send them to support."
}

func isOffline(err error) bool {
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "failed to fetch") || strings.Contains(s, "networkerror") || strings.Contains(s, "network error")
}

func ErrorView(id string, err error) xango.Node {
	info := ExplainError(err)
	attrs := []xango.Attr{xango.A("class", "gj-doc-error-box")}
	if id != "" {
		attrs = append(attrs, xango.A("id", id))
	}
	return xango.El("div", attrs, errorParts(info)...)
}

func errorParts(info ErrorInfo) []xango.Node {
	parts := []xango.Node{xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error"), xango.A("role", "alert")}, xango.Text(info.Message))}
	if info.Details == "" || info.Details == info.Message {
		return parts
	}
	body := []xango.Node{
		xango.El("summary", nil, xango.Text(tr("err.details", "Details"))),
		xango.El("pre", []xango.Attr{xango.A("class", "gj-doc-error-text")}, xango.Text(info.Details)),
	}
	if copyText := CopyText; copyText != nil {
		text := info.Details
		body = append(body, docButton("gj-doc-error-copy", tr("err.copy", "Copy details"), false, func() { copyText(text) }))
	}
	return append(parts, xango.El("details", []xango.Attr{xango.A("class", "gj-doc-error-details")}, body...))
}
