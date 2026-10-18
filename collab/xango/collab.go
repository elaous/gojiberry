// Package xango is the shared-editing wrapper for canvas xango trees (import path
// collab/xango): put canvas elements inside <collab.Collab> and they become
// multi-user.
//
//	import (
//		canvas "github.com/elaous/gojiberry/canvas/xango"
//		collab "github.com/elaous/gojiberry/collab/xango"
//	)
//
//	<canvas.Canvas width={800} height={600} mode={canvas.ModeEdit}>
//		<collab.Collab
//			transport={collab.WebSockets()}
//			client={client.Options{BaseURL: "https://host", Doc: "report"}}
//			name="Ana" color="#e11d48">
//			<canvas.TextFlow items={seed} pageCount={3} />
//		</collab.Collab>
//	</canvas.Canvas>
//
// The wrapper connects when it appears, shares the flow it wraps with
// everyone on the document, applies what they do, draws their cursors,
// follows the role the server gives (a viewer's canvas is held in view
// mode), repairs itself when the server refuses something, and disconnects
// when it leaves the tree. How it talks to the hub is the Transport: HTTP
// endpoints (Server-Sent Events and POSTs, the default) or one WebSocket.
//
// While wrapped, the flow's content belongs to the session: the content props
// (Items, Source, Text) only seed an empty document, and changing them later
// does not push anything.
//
// The wrapper runs a goroutine that polls the session and touches the flow
// and the stage, as the canvas's own event handlers do: that is safe in the
// single-threaded wasm runtime. Elsewhere give Sync, which runs a function on
// the goroutine that owns them.
package xango

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/collab"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/xango"
)

// TagCollab is the element's tag.
const TagCollab = "collab.Collab"

// Flow says which part of the wrapped canvas is shared.
type Flow string

const (
	// FlowText is the main text flow (a canvas.TextFlow element): the first
	// one inside the wrapper, with its comments, notes and styles. This is
	// the default.
	FlowText Flow = "textflow"
	// FlowHeader and FlowFooter are the pages' headers, and footers with
	// their page numbers.
	FlowHeader Flow = "header"
	FlowFooter Flow = "footer"
	// FlowObjects is the elements placed on the pages: floating text,
	// tables and images (their position and size, and the text or cells).
	FlowObjects Flow = "objects"
	// FlowAll is all of the above.
	FlowAll Flow = "*"
)

// ErrFlowNotSupported is reported (State.Err) for a Flow that is not one of
// the constants above.
var ErrFlowNotSupported = errors.New("collab: unknown flow (want textflow, header, footer, objects or *)")

// ErrNoFlow is reported when the wrapper has no canvas.TextFlow inside.
var ErrNoFlow = errors.New("collab: no canvas.TextFlow inside the Collab element")

// State is what the wrapper tells OnState: the connection, who else is on the
// document and what is waiting to be sent.
type State struct {
	Replica string // this session's own id: the others' Peers are the ones that are not it
	Status  client.Status
	Role    wire.Role
	Peers   []wire.Presence // the other people, by name
	Pending int             // operations the server has not accepted yet
	Repairs int             // times the session had to be rebuilt
	// Merged counts the changes made while offline that have met the shared document since the session
	// began; Rejected the changes the server refused (see collab.Session). Both only grow.
	Merged, Rejected int
	// Conflicts counts the times others changed a paragraph being edited here, ConflictWith names them for the
	// latest (see collab.Session). Only grows.
	Conflicts    int
	ConflictWith []string
	// Err is why sharing is not working: the flow is not supported, or has
	// content that cannot be shared, or the server refused something and
	// the session has not yet repaired itself.
	Err error
}

// CollabProps configures the wrapper.
type CollabProps struct {
	Key string

	// Transport is how the session talks to the hub: collab.HTTPEndpoints()
	// (the default) or ws.WebSockets() from collab/ws, or your own.
	Transport collab.Transport
	// Client says where the hub is (BaseURL, Doc), as whom (Header, for
	// the sign-in token) and how to retry.
	Client client.Options
	// Replica identifies this copy of the document; it must be unique per
	// tab or device. The default is random.
	Replica string
	// Name and Color are shown to others with the cursor.
	Name, Color string

	// Flow chooses what is shared (default FlowText).
	Flow Flow

	// Interval is how often the session is polled (default 40ms).
	Interval time.Duration
	// OnState is called when the connection, the people, the role or the
	// waiting operations change; from the polling goroutine (or from Sync).
	OnState func(State)
	// Holding, when set, is asked every tick for the object this person is changing ("" for none), which goes to the
	// others with the cursor and is renewed while it is held.
	Holding func() string
	// TypedFor, when set, is asked every tick for how long the text others typed stays in their colour (zero is the
	// default, collab.DefaultTypedFor; negative turns the colours off).
	TypedFor func() time.Duration
	// Sync, if set, runs f on the goroutine that owns the flow and the
	// stage; the polling loop does all its work through it.
	Sync func(f func())

	Children []xango.Node
}

// Collab wraps canvas elements and makes them shared.
func Collab(p CollabProps) xango.Node { return xango.Host(TagCollab, p, p.Children...) }

func init() {
	cxango.Register(TagCollab, func() cxango.Element { return &element{} })
}

// element is the live wrapper.
type element struct {
	kids  cxango.Children
	props CollabProps
	has   bool
	ctx   *cxango.Context

	flow *canvas.TextFlow
	id   string // what the running session was made from
	run  *runner
	err  error
}

// Drawers implements cxango.Element: the wrapper draws what its children draw.
func (e *element) Drawers() []canvas.Drawer { return e.kids.Drawers() }

// Elements implements cxango.Holder.
func (e *element) Elements() []cxango.Element { return e.kids.Elements() }

// Close implements cxango.Closer: disconnect and close the children.
func (e *element) Close() {
	e.stop()
	e.kids.Close()
}

func (e *element) stop() {
	if e.run != nil {
		e.run.stop()
		e.run = nil
	}
	e.id = ""
}

// Update implements cxango.Element.
func (e *element) Update(c *cxango.Context, props any, children []xango.Node) (bool, error) {
	p, ok := props.(CollabProps)
	if !ok {
		return false, fmt.Errorf("collab/xango: Collab got props of type %T", props)
	}
	if p.Replica == "" {
		if e.props.Replica != "" {
			p.Replica = e.props.Replica // keep the generated one across renders
		} else {
			p.Replica = randomReplica()
		}
	}
	e.ctx = c
	changed, _, err := e.kids.Update(c, children)
	if err != nil {
		return false, err
	}
	e.props, e.has = p, true

	flow, _ := e.kids.Find(cxango.TagTextFlow, 0).(*canvas.TextFlow)
	want := identity(p, flow)
	if want != e.id || flow != e.flow {
		e.stop()
		e.flow = flow
		e.start(p, flow, want)
	} else if e.run != nil {
		e.run.props.Store(&p) // callbacks and the interval may change without a reconnect
	}
	return changed, nil
}

// start connects, or reports why it cannot.
func (e *element) start(p CollabProps, flow *canvas.TextFlow, id string) {
	report := func(err error) {
		if err == e.err {
			return // said already; a re-render must not repeat it
		}
		e.err = err
		if p.OnState != nil {
			p.OnState(State{Err: err, Status: client.Offline})
		}
	}
	if flow == nil {
		report(ErrNoFlow)
		return
	}
	sharing, ok := p.Flow.sharing(&e.kids)
	if !ok {
		report(ErrFlowNotSupported)
		return
	}
	opts := p.Client
	sess, err := collab.ConnectShared(p.Transport, p.Replica, flow, sharing, opts)
	if err != nil {
		report(err)
		return
	}
	sess.Name, sess.Color = p.Name, p.Color
	e.err, e.id = nil, id
	e.run = newRunner(e.ctx, sess, flow, &p)
}

// identity is what a running session was made from: when it changes the
// wrapper reconnects.
func identity(p CollabProps, flow *canvas.TextFlow) string {
	hdr := make([]string, 0, len(p.Client.Header))
	for k, v := range p.Client.Header {
		hdr = append(hdr, fmt.Sprint(k, v))
	}
	sort.Strings(hdr)
	return fmt.Sprint(p.Key, "|", p.Client.BaseURL, "|", p.Client.Doc, "|", p.Replica, "|", p.Flow, "|", hdr, "|", transportID(p.Transport), "|", flow != nil)
}

// transportID tells two transports apart without comparing values that may
// not be comparable (a TransportFunc).
func transportID(t collab.Transport) string {
	if t == nil {
		return "http"
	}
	v := reflect.ValueOf(t)
	switch v.Kind() {
	case reflect.Func, reflect.Pointer, reflect.Map, reflect.Chan, reflect.Slice:
		return fmt.Sprintf("%T:%x", t, v.Pointer())
	}
	return fmt.Sprintf("%T:%v", t, t)
}

func randomReplica() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprint("r", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
