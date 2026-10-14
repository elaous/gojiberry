package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

// envState is the file the built-in totals are kept in across restarts. Unset, they start from zero at
// every start, as they always did.
const envState = "GOJIBERRY_OBS_STATE"

// stateEvery is how often the totals are written while the server runs; they are also written when it
// stops, so a crash loses at most this much.
const stateEvery = 5 * time.Minute

// persisted is the file: the operator's series and the span statistics, nothing else. It is the same
// numbers GET /admin/observe shows, so it holds no more than that page does (no ids, names or text:
// every label is a member of a closed set, which Restore checks again).
type persisted struct {
	Version  int        `json:"v"`
	Series   []Series   `json:"series"`
	Spans    []SpanStat `json:"spans"`
	Overflow int64      `json:"overflow,omitempty"`
}

// MarshalState is the totals as the file holds them.
func (b *Builtin) MarshalState() []byte {
	series, spans, over := b.Snapshot()
	out, _ := json.Marshal(persisted{Version: 1, Series: series, Spans: spans, Overflow: over})
	return out
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// RestoreState reads totals written by MarshalState into an empty sink and adds them to what it holds. A
// file that is not exactly what MarshalState writes (an unknown field, a metric or label value that is not
// on the allowlist, a negative count, a number that is not finite, more series than the sink keeps) is
// refused whole and nothing is restored: a damaged or tampered file starts the totals again, it never
// puts a value in them that the allowlist would have refused.
func (b *Builtin) RestoreState(data []byte) error {
	var p persisted
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || dec.More() {
		return errors.New("observe: the stored totals are not totals")
	}
	if p.Version != 1 || len(p.Series) > maxSeries || p.Overflow < 0 {
		return errors.New("observe: the stored totals are of another shape")
	}
	aggs := map[string]*agg{}
	for _, s := range p.Series {
		d, ok := defByName[s.Metric]
		if !ok || s.Source != Operator.String() || s.Kind != d.Kind.String() || len(s.Labels) != len(d.Labels) {
			return fmt.Errorf("observe: the stored totals hold a series (%q) that is not on the allowlist", s.Metric)
		}
		labels := make([]Label, len(d.Labels))
		for i, name := range d.Labels {
			v, ok := s.Labels[name]
			if !ok || usageLabels[name] || !labelAllowed[name][v] {
				return fmt.Errorf("observe: the stored totals hold a label value of %q that is not on the allowlist", s.Metric)
			}
			labels[i] = Label{name, v}
		}
		if s.Count < 0 || !finite(s.Sum, s.Min, s.Max, s.Last) || (d.Kind != Gauge && (s.Sum < 0 || s.Min < 0)) {
			return fmt.Errorf("observe: the stored totals hold a bad number for %q", s.Metric)
		}
		pt := Point{Metric: s.Metric, Kind: d.Kind, Source: Operator, Labels: labels}
		k := seriesKey(pt)
		if aggs[k] != nil {
			return fmt.Errorf("observe: the stored totals hold %q twice", s.Metric)
		}
		a := newAgg(pt)
		a.count, a.sum, a.min, a.max, a.last = s.Count, s.Sum, s.Min, s.Max, s.Last
		aggs[k] = a
	}
	spans := map[SpanName]*SpanStat{}
	for _, st := range p.Spans {
		if _, ok := spanByName[st.Name]; !ok || spans[st.Name] != nil || st.Count < 0 || st.Errors < 0 || st.Errors > st.Count || !finite(st.TotalSeconds, st.MaxSeconds) || st.TotalSeconds < 0 || st.MaxSeconds < 0 {
			return fmt.Errorf("observe: the stored totals hold a bad span (%q)", st.Name)
		}
		c := st
		spans[st.Name] = &c
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.series)+len(aggs) > maxSeries {
		return errors.New("observe: the stored totals do not fit")
	}
	for k, a := range aggs {
		if cur := b.series[k]; cur != nil {
			cur.count += a.count
			cur.sum += a.sum
			cur.min, cur.max = math.Min(cur.min, a.min), math.Max(cur.max, a.max)
			continue
		}
		b.series[k] = a
	}
	for n, st := range spans {
		if cur := b.spans[n]; cur != nil {
			cur.Count += st.Count
			cur.TotalSeconds += st.TotalSeconds
			cur.Errors += st.Errors
			cur.MaxSeconds = math.Max(cur.MaxSeconds, st.MaxSeconds)
			continue
		}
		b.spans[n] = st
	}
	b.overflow += p.Overflow
	return nil
}

// KeepIn makes the sink keep its totals in the file at path: it reads them now (a missing file is the first
// start; a damaged one is reported and the totals start again), writes them every few minutes, and writes
// them when it is closed. The file is written beside its name and renamed, with mode 0600.
func (b *Builtin) KeepIn(path string) error {
	b.mu.Lock()
	b.path = path
	b.mu.Unlock()
	var readErr error
	if data, err := os.ReadFile(path); err == nil {
		if err := b.RestoreState(data); err != nil {
			readErr = err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		readErr = fmt.Errorf("observe: the stored totals could not be read: %w", err)
	}
	b.stop = make(chan struct{})
	b.done = make(chan struct{})
	go func() {
		defer close(b.done)
		t := time.NewTicker(stateEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = b.save()
			case <-b.stop:
				return
			}
		}
	}()
	return readErr
}

func (b *Builtin) save() error {
	b.mu.Lock()
	path := b.path
	b.mu.Unlock()
	if path == "" {
		return nil
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, b.MarshalState(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// closeState stops the writer and writes the totals one last time.
func (b *Builtin) closeState(ctx context.Context) error {
	if b.stop == nil {
		return nil
	}
	select {
	case <-b.stop:
	default:
		close(b.stop)
	}
	select {
	case <-b.done:
	case <-ctx.Done():
	}
	return b.save()
}
