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
	// FlowText is the main text flow.
	FlowText Flow = "textflow"
	// FlowHeader and FlowFooter are the pages' headers and footers.
	FlowHeader Flow = "header"
	FlowFooter Flow = "footer"
	// FlowObjects is the elements placed on the pages.
	FlowObjects Flow = "objects"
	// FlowAll is all of the above.
	FlowAll Flow = "*"
)

// ErrFlowNotSupported is reported for a Flow the element cannot share.
var ErrFlowNotSupported = errors.New("collab: unknown flow (want textflow, header, footer, objects or *)")

// ErrNoFlow is reported when the wrapper has no canvas.
var ErrNoFlow = errors.New("collab: no canvas.TextFlow inside the Collab element")

// State is what the wrapper tells OnState.
type State struct {
	Replica string
	Status  client.Status
	Role    wire.Role
	Peers   []wire.Presence // the other people, by name
	Pending int             // operations the server has not accepted yet
	Repairs int             // times the session had to be rebuilt
	// Merged counts changes made while offline and merged later.
	Merged, Rejected int
	// Conflicts counts times others changed a paragraph edited here.
	Conflicts    int
	ConflictWith []string
	// Err is why sharing is not working.
	Err error
}

// CollabProps configures the wrapper.
type CollabProps struct {
	Key string

	// Transport is how the session talks to the hub.
	Transport collab.Transport
	// Client says where the hub is.
	Client client.Options
	// Replica identifies this copy of the document.
	Replica string
	// Name and Color are shown to others with the cursor.
	Name, Color string

	// Flow chooses what is shared; the default is FlowText.
	Flow Flow

	// Interval is how often the session is polled.
	Interval time.Duration
	// OnState is called when the connection or state changes.
	OnState func(State)
	Holding func() string
	// TypedFor, when set, gives how long typed text stays marked.
	TypedFor func() time.Duration
	// Sync, if set, runs f on the session goroutine.
	Sync func(f func())

	Children []xango.Node
}

// Collab wraps canvas elements and makes them shared.
func Collab(p CollabProps) xango.Node { return xango.Host(TagCollab, p, p.Children...) }

func init() {
	cxango.Register(TagCollab, func() cxango.Element { return &element{} })
}

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

// Drawers implements cxango.Element.
func (e *element) Drawers() []canvas.Drawer { return e.kids.Drawers() }

// Elements implements cxango.Holder.
func (e *element) Elements() []cxango.Element { return e.kids.Elements() }

// Close implements cxango.Closer: disconnects and closes.
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
		e.run.props.Store(&p)
	}
	return changed, nil
}

func (e *element) start(p CollabProps, flow *canvas.TextFlow, id string) {
	report := func(err error) {
		if err == e.err {
			return
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

func identity(p CollabProps, flow *canvas.TextFlow) string {
	hdr := make([]string, 0, len(p.Client.Header))
	for k, v := range p.Client.Header {
		hdr = append(hdr, fmt.Sprint(k, v))
	}
	sort.Strings(hdr)
	return fmt.Sprint(p.Key, "|", p.Client.BaseURL, "|", p.Client.Doc, "|", p.Replica, "|", p.Flow, "|", hdr, "|", transportID(p.Transport), "|", flow != nil)
}

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
