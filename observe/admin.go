package observe

import (
	"encoding/json"
	"net/http"
	"sort"
)

var NeverSent = []string{
	"IP addresses or any other network address",
	"user, document, key, share or session identifiers",
	"tokens, cookies, headers, the query string or the body of a request",
	"the user agent",
	"raw request paths (only route templates such as /docs/{id})",
	"free text of any kind, including error messages",
}

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
}

// Describe reports the configuration and allowlist.
func (o *Observer) Describe() Description {
	d := Description{
		Available: Registered(),
		Metrics:   nil,
		Spans:     Spans(), Attributes: Attributes(),
		NeverSent: NeverSent,
		Dropped:   o.Dropped(),
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

// WriteAdmin writes the description as JSON.
func WriteAdmin(w http.ResponseWriter, o *Observer) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(o.Describe())
}
