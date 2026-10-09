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

// ErrorKind says in which user-facing group an error falls. A group is what the person can do about
// it, not which Go value it was.
type ErrorKind string

// The groups ExplainError sorts errors into.
const (
	// ErrKindApp is an error the host already put into words (it has a UserMessage).
	ErrKindApp ErrorKind = "app"
	// ErrKindUnsigned is a file saved unsigned while a key change finished.
	ErrKindUnsigned ErrorKind = "unsigned"
	// ErrKindForged is a file that no owner of the document wrote.
	ErrKindForged ErrorKind = "forged"
	// ErrKindNoSigner is a save that needs the owner's signature, which this device cannot give.
	ErrKindNoSigner ErrorKind = "nosigner"
	// ErrKindRolledBack is a document or list older than one seen before.
	ErrKindRolledBack ErrorKind = "rolledback"
	// ErrKindUnreadable is a stored file that cannot be opened here.
	ErrKindUnreadable ErrorKind = "unreadable"
	// ErrKindNoKey is a document that none of the unlocked keys opens.
	ErrKindNoKey ErrorKind = "nokey"
	// ErrKindLocked is a store or document that is locked.
	ErrKindLocked ErrorKind = "locked"
	// ErrKindConflict is a document changed elsewhere since it was read.
	ErrKindConflict ErrorKind = "conflict"
	// ErrKindFloor is the browser refusing to keep the safety record.
	ErrKindFloor ErrorKind = "floor"
	// ErrKindStorageFull is a store with no room left.
	ErrKindStorageFull ErrorKind = "storagefull"
	// ErrKindRecovery is a key set that needs a recovery key or an SSH key too.
	ErrKindRecovery ErrorKind = "recovery"
	// ErrKindOffline is a request that did not reach its server.
	ErrKindOffline ErrorKind = "offline"
	// ErrKindUnknown is everything else.
	ErrKindUnknown ErrorKind = "unknown"
)

// UserMessager is implemented by an error whose text is already the words to show a person (in
// their language). ExplainError shows that text and keeps the error's own text as the details.
type UserMessager interface{ UserMessage() string }

// ErrorInfo is an error as a person reads it.
type ErrorInfo struct {
	Kind ErrorKind
	// Message is plain, translated and says what to do next. It names no Go value and no API call.
	Message string
	// Details is the technical text, for support.
	Details string
}

// CopyText puts text on the clipboard. The host sets it; while it is nil the details have no Copy
// button (the text stays selectable).
var CopyText func(text string)

// ErrorGroup is a group of errors a host (or a package that knows more errors than this one) adds.
type ErrorGroup struct {
	Kind ErrorKind
	// ID is the message id for Translate and Default its English text.
	ID, Default string
	// Match says whether err belongs to the group.
	Match func(err error) bool
	// As, when set, files the group under that error's own message (ID and Default are not used).
	As error
}

var errorGroups []ErrorGroup

// RegisterErrorGroups adds groups that ExplainError tries before its own, in the order given (the most
// specific first). Call it once, before the first render. It exists so that a package can teach
// ExplainError the errors of a layer these components do not import (package components/encerr does it for
// the encrypting adapter).
func RegisterErrorGroups(g ...ErrorGroup) { errorGroups = append(errorGroups, g...) }

// ExplainError sorts err into a group with a translated message. The order matters where one error
// wraps another: the most specific group is tried first. A nil err gives the zero ErrorInfo.
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

// isOffline says the request did not get an answer: no network, a refused or timed-out connection,
// or the browser's fetch failure (which carries no typed error).
func isOffline(err error) bool {
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "failed to fetch") || strings.Contains(s, "networkerror") || strings.Contains(s, "network error")
}

// ErrorView draws err for a person: the message (an alert, so a screen reader says it at once) and,
// under a native Details disclosure, the technical text with a Copy button when CopyText is set.
// The id is the element that holds both.
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
