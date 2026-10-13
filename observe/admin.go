package observe

import (
	"encoding/json"
	"net/http"
	"sort"
)

// NeverSent lists what no sink ever gets, in words, for the admin route and the docs. A test fuzzes
// requests to hold the code to it.
var NeverSent = []string{
	"IP addresses or any other network address",
	"user, document, key, share or session identifiers",
	"tokens, cookies, headers, the query string or the body of a request",
	"the user agent",
	"raw request paths (only route templates such as /docs/{id})",
	"free text of any kind, including error messages",
}

// Description is what GET /admin/observe answers: the allowlist, which sinks are on and, for the sinks
// that send, the shape of the last thing they sent, so an operator can see what would leave before
// enabling anything. It holds no secret.
type Description struct {
	Enabled     bool                `json:"enabled"`
	Sinks       []string            `json:"sinks"`
	UsageSinks  []string            `json:"usage_sinks"`
	Available   []string            `json:"available_sinks"`
	TraceSample float64             `json:"trace_sample"`
	Metrics     []MetricInfo        `json:"metrics"`
	Spans       []SpanInfo          `json:"spans"`
	Attributes  []AttributeInfo     `json:"attributes"`
	NeverSent   []string            `json:"never_sent"`
	Dropped     map[string]int64    `json:"dropped"`
	SinkStatus  map[string]any      `json:"sink_status,omitempty"`
	Usage       map[string][]string `json:"usage_allowlist,omitempty"`
	// Example is the shape of a PostHog request, with made-up numbers, shown whether or not that sink is on.
	Example map[string]any `json:"example_posthog_payload"`
}

// Describe reports the configuration and allowlist. It works on a nil Observer (observability off),
// which is how an operator inspects it before turning it on.
func (o *Observer) Describe() Description {
	d := Description{
		Available: Registered(),
		Metrics:   nil,
		Spans:     Spans(), Attributes: Attributes(),
		NeverSent: NeverSent,
		Dropped:   o.Dropped(),
		Example:   ExamplePostHogPayload(),
		Sinks:     []string{}, UsageSinks: []string{},
	}
	var usage map[string][]string
	if o != nil {
		o.mu.Lock()
		usage = map[string][]string{}
		for f, vs := range o.usageAllow {
			for v := range vs {
				usage[f] = append(usage[f], v)
			}
			sort.Strings(usage[f])
		}
		o.mu.Unlock()
	}
	d.Metrics = Metrics(usage)
	d.Usage = usage
	if o == nil || o.cfg == nil {
		return d
	}
	d.Enabled = true
	d.Sinks, d.UsageSinks, d.TraceSample = o.cfg.Sinks, o.cfg.UsageSinks, o.cfg.TraceSample
	d.SinkStatus = map[string]any{}
	for n, s := range o.named {
		if ds, ok := s.(Describer); ok {
			d.SinkStatus[n] = ds.Describe()
		}
	}
	return d
}

// WriteAdmin writes the description as JSON. The caller has already checked that the request comes
// from the operator.
func WriteAdmin(w http.ResponseWriter, o *Observer) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(o.Describe())
}
