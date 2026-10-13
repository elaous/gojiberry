package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func optedIn(t *testing.T) *Collector {
	t.Helper()
	c := NewCollector(func() time.Time { return noon })
	if err := c.SetConsent(true); err != nil {
		t.Fatal(err)
	}
	return c
}

func okSend(context.Context, []byte) error { return nil }

func TestSamplingSendsFromOneVisitInN(t *testing.T) {
	in, out := 0, 0
	for i := 0; i < 1000; i++ {
		c := NewCollector(nil)
		c.SetSampling(10, nil)
		_ = c.SetConsent(true)
		if c.Sampled() {
			in++
		} else {
			out++
		}
	}
	if in < 50 || in > 160 { // 100 expected; 8 standard deviations of slack would be silly, this is plenty
		t.Errorf("%d of 1000 visits in a 1 in 10 sample", in)
	}

	// A visit that is not in the sample records nothing, shows nothing and sends nothing.
	c := NewCollector(nil)
	c.SetSampling(10, func(int) int { return 3 })
	_ = c.SetConsent(true)
	_ = c.Record(Feature, "find")
	if c.Sampled() || c.Body() != nil || c.Preview() != nil {
		t.Fatal("a visit outside the sample keeps or shows counts")
	}
	calls := 0
	n, err := c.Flush(context.Background(), func(context.Context, []byte) error { calls++; return nil })
	if n != 0 || err != nil || calls != 0 {
		t.Fatalf("flush outside the sample: %d %v, %d calls", n, err, calls)
	}
	// One inside sends as before.
	c.SetSampling(10, func(int) int { return 0 })
	_ = c.Record(Feature, "find")
	if n, _ := c.Flush(context.Background(), okSend); n != 1 {
		t.Errorf("inside the sample sent %d", n)
	}
	// One in one is every visit; a nonsense rate means every visit.
	c.SetSampling(0, func(int) int { return 7 })
	if !c.Sampled() || c.SampleOneIn() != 1 {
		t.Error("rate 0 is not every visit")
	}
	// A new opt-in is a new draw.
	draws := 0
	c = NewCollector(nil)
	c.SetSampling(4, func(int) int { draws++; return 0 })
	_ = c.SetConsent(true)
	_ = c.SetConsent(false)
	_ = c.SetConsent(true)
	if draws != 3 { // the rate, then each opt-in
		t.Errorf("%d draws", draws)
	}
}

func TestTheLocalLogIsOffByDefaultAndHoldsOnlyWhatWouldBeSent(t *testing.T) {
	c := optedIn(t)
	_ = c.Record(Feature, "find")
	_, _ = c.Flush(context.Background(), okSend)
	if len(c.Log()) != 0 || c.LogOn() {
		t.Fatal("the log was on by default")
	}

	c.SetLog(true)
	_ = c.Record(Feature, "find")
	_ = c.Record(Locale, "de")
	_, _ = c.Flush(context.Background(), okSend)
	_ = c.Record(Feature, "share")
	_, _ = c.Flush(context.Background(), func(context.Context, []byte) error { return errors.New("down") })
	c.Withhold(OutcomeNoServer)
	log := c.Log()
	if len(log) != 3 || log[0].Outcome != OutcomeSent || log[1].Outcome != OutcomeFailed || log[2].Outcome != OutcomeNoServer {
		t.Fatalf("log: %+v", log)
	}
	if log[0].Time != "2030-05-01T12:00Z" || log[0].Day != "2030-05-01" || len(log[0].Counts) != 2 {
		t.Errorf("entry: %+v", log[0])
	}
	// Every entry is a batch the receiver would take: nothing else can be in the log.
	for _, e := range log {
		if err := (Batch{Day: e.Day, Counts: e.Counts}).Validate(noon); err != nil {
			t.Errorf("an entry is not a valid batch: %v: %+v", err, e)
		}
	}
	var file struct {
		Note    string
		Entries []map[string]any
	}
	if err := json.Unmarshal(c.LogJSON(), &file); err != nil || len(file.Entries) != 3 || !strings.Contains(file.Note, "never sent") {
		t.Fatalf("the downloadable log: %v %s", err, c.LogJSON())
	}
	for _, e := range file.Entries {
		for k := range e {
			if k != "time" && k != "outcome" && k != "day" && k != "counts" {
				t.Errorf("a log entry has the field %q", k)
			}
		}
	}

	// A copy: changing what Log returns changes nothing inside.
	log[0].Counts[0].N = 99
	if c.Log()[0].Counts[0].N == 99 {
		t.Error("Log returned the collector's own slice")
	}
	// Turning the log off, or consent off, forgets it.
	c.SetLog(false)
	if len(c.Log()) != 0 {
		t.Error("the log survived being turned off")
	}
	c.SetLog(true)
	_ = c.Record(Feature, "find")
	_, _ = c.Flush(context.Background(), okSend)
	_ = c.SetConsent(false)
	if len(c.Log()) != 0 {
		t.Error("the log survived the end of consent")
	}
}

func TestTheLogIsBounded(t *testing.T) {
	c := optedIn(t)
	c.SetLog(true)
	for i := 0; i < MaxLog+50; i++ {
		_ = c.Record(Feature, "find")
		_, _ = c.Flush(context.Background(), okSend)
	}
	if got := len(c.Log()); got != MaxLog {
		t.Errorf("%d entries, want %d", got, MaxLog)
	}
}

func TestOutsideTheSampleTheLogStillShowsWhatWouldHaveBeenSent(t *testing.T) {
	c := optedIn(t)
	c.SetSampling(5, func(int) int { return 2 })
	c.SetLog(true)
	_ = c.Record(Feature, "find")
	if c.Body() != nil {
		t.Fatal("a visit outside the sample has a body")
	}
	calls := 0
	_, _ = c.Flush(context.Background(), func(context.Context, []byte) error { calls++; return nil })
	log := c.Log()
	if calls != 0 || len(log) != 1 || log[0].Outcome != OutcomeNotSampled || len(log[0].Counts) != 1 {
		t.Fatalf("calls %d, log %+v", calls, log)
	}
	// Without the log, counts that wait for a server are kept by Withhold's absence of effect.
	d := optedIn(t)
	_ = d.Record(Feature, "find")
	d.Withhold(OutcomeNoServer)
	if d.Body() == nil {
		t.Error("Withhold dropped the counts although the log is off")
	}
}

// memStore is a Store in memory.
type memStore struct {
	mu    sync.Mutex
	data  []byte
	saves int
	err   error
}

func (m *memStore) Load(context.Context) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data, m.data != nil, m.err
}
func (m *memStore) Save(_ context.Context, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = append([]byte(nil), b...)
	m.saves++
	return m.err
}
func (m *memStore) count() int { m.mu.Lock(); defer m.mu.Unlock(); return m.saves }

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTotalsSurviveARestartAndABurstIsOneWrite(t *testing.T) {
	st := &memStore{}
	c := NewCounters()
	if err := c.Persist(context.Background(), st, 30*time.Millisecond, func() time.Time { return noon }); err != nil {
		t.Fatal(err)
	}
	r := NewReceiver(c).WithClock(func() time.Time { return noon })
	for i := 0; i < 20; i++ {
		post(r, `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":3}]}`)
	}
	post(r, `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":3}],"who":"me"}`) // refused
	waitFor(t, "the save", func() bool { return st.count() >= 1 })
	time.Sleep(80 * time.Millisecond)
	if st.count() > 2 {
		t.Errorf("a burst of 21 requests made %d writes", st.count())
	}

	// A new process with the same store has the totals.
	c2 := NewCounters()
	if err := c2.Persist(context.Background(), st, 30*time.Millisecond, func() time.Time { return noon }); err != nil {
		t.Fatal(err)
	}
	if got := total(c2, 1); got != 60 {
		t.Errorf("after the restart the total is %d, want 60", got)
	}
	if tt := c2.Totals(); tt.Accepted != 20 || tt.Refused != 1 {
		t.Errorf("request totals after the restart: %+v", tt)
	}
	// And it keeps counting on top of them.
	post(NewReceiver(c2).WithClock(func() time.Time { return noon }), `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":2}]}`)
	if got := total(c2, 1); got != 62 {
		t.Errorf("total %d, want 62", got)
	}
}

func TestTheSnapshotHoldsNothingButTheTotals(t *testing.T) {
	c := NewCounters()
	c.Add(Batch{Day: "2030-05-01", Counts: []Count{{Feature, "find", 7}, {Locale, "de", 2}}}, noon)
	var raw map[string]any
	if err := json.Unmarshal(c.Snapshot(), &raw); err != nil {
		t.Fatal(err)
	}
	for k := range raw {
		switch k {
		case "v", "seed", "accepted", "refused", "days":
		default:
			t.Errorf("the snapshot has a field %q", k)
		}
	}
	if len(c.Snapshot()) > 4<<10 {
		t.Errorf("a snapshot of two totals is %d bytes", len(c.Snapshot()))
	}
}

func TestRestoreRefusesWhatARequestCouldNotHaveCounted(t *testing.T) {
	good := `{"v":1,"accepted":1,"refused":0,"days":{"2030-04-30":[{"name":"feature","value":"find","n":3}]}}`
	c := NewCounters()
	if err := c.Restore([]byte(good), noon); err != nil {
		t.Fatalf("a good snapshot: %v", err)
	}
	for name, bad := range map[string]string{
		"extra field":      `{"v":1,"accepted":0,"refused":0,"days":{},"user":"x"}`,
		"extra total":      `{"v":1,"accepted":0,"refused":0,"days":{"2030-04-30":[{"name":"feature","value":"find","n":3,"id":"u"}]}}`,
		"unknown value":    `{"v":1,"accepted":0,"refused":0,"days":{"2030-04-30":[{"name":"feature","value":"secret-doc","n":3}]}}`,
		"unknown name":     `{"v":1,"accepted":0,"refused":0,"days":{"2030-04-30":[{"name":"user","value":"find","n":3}]}}`,
		"bad day":          `{"v":1,"accepted":0,"refused":0,"days":{"yesterday":[]}}`,
		"zero total":       `{"v":1,"accepted":0,"refused":0,"days":{"2030-04-30":[{"name":"feature","value":"find","n":0}]}}`,
		"huge total":       `{"v":1,"accepted":0,"refused":0,"days":{"2030-04-30":[{"name":"feature","value":"find","n":99999999999999}]}}`,
		"duplicate":        `{"v":1,"accepted":0,"refused":0,"days":{"2030-04-30":[{"name":"feature","value":"find","n":1},{"name":"feature","value":"find","n":1}]}}`,
		"negative":         `{"v":1,"accepted":-1,"refused":0,"days":{}}`,
		"other version":    `{"v":2,"accepted":0,"refused":0,"days":{}}`,
		"bad seed":         `{"v":1,"seed":"zz","accepted":0,"refused":0,"days":{}}`,
		"trailing garbage": good + `{}`,
		"not json":         `hello`,
	} {
		before := total(c, 1)
		if err := c.Restore([]byte(bad), noon); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if total(c, 1) != before {
			t.Errorf("%s changed the totals although it was refused", name)
		}
	}
	if err := c.Restore(make([]byte, MaxSnapshot+1), noon); err == nil {
		t.Error("an oversized snapshot was accepted")
	}
	// A day past the retention is dropped, as it would have been.
	old := `{"v":1,"accepted":0,"refused":0,"days":{"2020-01-01":[{"name":"feature","value":"find","n":3}]}}`
	c2 := NewCounters()
	if err := c2.Restore([]byte(old), noon); err != nil || total(c2, 1) != 0 {
		t.Errorf("an old day: %v, total %d", err, total(c2, 1))
	}
}

func TestAStoreThatCannotBeReadDoesNotStopTheCounting(t *testing.T) {
	st := &memStore{data: []byte("garbage")}
	c := NewCounters()
	if err := c.Persist(context.Background(), st, 20*time.Millisecond, nil); err == nil {
		t.Error("a damaged snapshot was not reported")
	}
	c.Add(Batch{Day: noon.Format(DayFormat), Counts: []Count{{Feature, "find", 5}}}, noon)
	waitFor(t, "the damaged snapshot to be replaced", func() bool { return st.count() >= 1 })
	if err := NewCounters().Restore(st.data, noon); err != nil {
		t.Errorf("the replacement is not a snapshot: %v", err)
	}
	failing := &memStore{err: errors.New("disk full")}
	c = NewCounters()
	if err := c.Persist(context.Background(), failing, 20*time.Millisecond, nil); err == nil {
		t.Error("a store error was not reported")
	}
	c.Add(Batch{Day: noon.Format(DayFormat), Counts: []Count{{Feature, "find", 5}}}, noon)
	waitFor(t, "the save error", func() bool { return c.SaveError() != nil })
	if total(c, 1) != 5 {
		t.Error("counting stopped when the store failed")
	}
}

func TestNoiseRoundsSmallCountsToTheStepAndIsStable(t *testing.T) {
	c := NewCounters()
	if err := c.SetNoise(5); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 40; n++ {
		c = NewCounters()
		_ = c.SetNoise(5)
		c.Add(Batch{Day: "2030-05-01", Counts: []Count{{Feature, "find", n}}}, noon)
		r := c.Report(1)
		again := c.Report(1)
		if len(again) != len(r) || (len(r) > 0 && again[0].N != r[0].N) {
			t.Fatalf("n=%d: the same question gave two answers", n)
		}
		got := 0
		if len(r) > 0 {
			got = r[0].N
		}
		if got%5 != 0 || got < n-4 || got > n+4 {
			t.Fatalf("n=%d reported as %d", n, got)
		}
		if n%5 == 0 && got != n {
			t.Errorf("a multiple of the step moved: %d -> %d", n, got)
		}
	}
}

func TestNoiseIsRightOnAverage(t *testing.T) {
	// Over many independent events (seeds), the mean of the reported number is the true one.
	const n, trials = 3, 4000
	sum := 0
	for i := 0; i < trials; i++ {
		c := NewCounters()
		_ = c.SetNoise(5)
		c.Add(Batch{Day: "2030-05-01", Counts: []Count{{Feature, "find", n}}}, noon)
		for _, row := range c.Report(1) {
			sum += row.N
		}
	}
	mean := float64(sum) / trials
	if mean < 2.7 || mean > 3.3 {
		t.Errorf("mean of the noisy report %.2f, want about %d", mean, n)
	}
}

func TestNoiseAppliesBeforeTheThresholdAndIsOffByDefault(t *testing.T) {
	c := NewCounters()
	c.Add(Batch{Day: "2030-05-01", Counts: []Count{{Feature, "find", 4}, {Feature, "share", 5}}}, noon)
	if got := c.Report(0); len(got) != 1 || got[0].N != 5 {
		t.Fatalf("without noise: %v", got)
	}
	_ = c.SetNoise(10)
	for _, row := range c.Report(0) {
		if row.N%10 != 0 || row.N < 10 {
			t.Errorf("a figure under the threshold or off the step: %v", row)
		}
	}
	_ = c.SetNoise(0)
	if c.Noise() != 0 || len(c.Report(0)) != 1 {
		t.Error("noise did not turn off")
	}
}

func TestNoiseSeedSurvivesARestartSoTheAnswerDoesNotChange(t *testing.T) {
	a := NewCounters()
	_ = a.SetNoise(5)
	for _, v := range []string{"find", "share", "spell", "people", "save"} {
		a.Add(Batch{Day: "2030-05-01", Counts: []Count{{Feature, v, 3}}}, noon)
	}
	b := NewCounters()
	_ = b.SetNoise(5)
	if err := b.Restore(a.Snapshot(), noon); err != nil {
		t.Fatal(err)
	}
	ra, rb := a.Report(0), b.Report(0)
	if len(ra) != len(rb) {
		t.Fatalf("%v vs %v", ra, rb)
	}
	for i := range ra {
		if ra[i] != rb[i] {
			t.Errorf("after a restart %v became %v", ra[i], rb[i])
		}
	}
}

func TestParseNoiseAndSetNoiseRefuseNonsense(t *testing.T) {
	for in, want := range map[string]int{"": 0, "off": 0, " 0 ": 0, "5": 5, "1000": 1000} {
		if got, err := ParseNoise(in); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"1", "-5", "1001", "five", "5.5", "on"} {
		if _, err := ParseNoise(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	if err := NewCounters().SetNoise(-1); err == nil {
		t.Error("a negative step was accepted")
	}
}
