package observe

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Environment variables (all start with GOJIBERRY_OBS_ except the switch itself). Errors name the
// variable at fault and never repeat a value. See docs/observability.md for the full table.
//
//	GOJIBERRY_OBS                      on | off. Default off, and unset is off: that is the kill switch.
//	GOJIBERRY_OBS_SINKS                where the operator's metrics and traces go: builtin, otlp, posthog
//	                                   (comma separated; default builtin)
//	GOJIBERRY_OBS_USAGE_SINKS          where the browsers' usage counts (package telemetry) are also
//	                                   sent, besides the admin pages that always have them: otlp, posthog
//	                                   (default none)
//	GOJIBERRY_OBS_STATE                a file the builtin totals are kept in across restarts (default none:
//	                                   they start from zero)
//	GOJIBERRY_OBS_TRACE_SAMPLE         share of requests that get a trace, 0 to 1 (default 0.1)
//	GOJIBERRY_OBS_POSTHOG_*            see PostHogConfig
//	OTEL_EXPORTER_OTLP_*               the standard OpenTelemetry variables, read by the otlp adapter
const (
	envSwitch      = "GOJIBERRY_OBS"
	envSinks       = "GOJIBERRY_OBS_SINKS"
	envUsageSinks  = "GOJIBERRY_OBS_USAGE_SINKS"
	envTraceSample = "GOJIBERRY_OBS_TRACE_SAMPLE"
)

// (envState, the file of the built-in totals, is in persist.go.)

// DefaultTraceSample is the share of requests that get a trace when GOJIBERRY_OBS_TRACE_SAMPLE is unset.
const DefaultTraceSample = 0.1

// ServiceName is the name sinks report this server as.
const ServiceName = "gojiberry-docstore"

// knownSinks are the names GOJIBERRY_OBS_SINKS accepts; whether a build can make one is another matter.
var knownSinks = []string{"builtin", "otlp", "posthog"}

// Config is the parsed environment.
type Config struct {
	// Enabled is GOJIBERRY_OBS=on.
	Enabled bool
	// Sinks are the sinks of the operator's metrics and traces, UsageSinks of the usage counts
	// (the built-in admin pages always have those, listed or not).
	Sinks, UsageSinks []string
	TraceSample       float64
	// StatePath is GOJIBERRY_OBS_STATE: the file the built-in totals are kept in ("" none).
	StatePath string
	// Notes are things the operator should hear at start (an ignored variable).
	Notes []string
}

func parseSinks(env, raw string, def []string) ([]string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return def, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range strings.Split(raw, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		known := false
		for _, k := range knownSinks {
			known = known || k == n
		}
		if !known {
			return nil, fmt.Errorf("observe: %s names a sink that does not exist (known: %s)", env, strings.Join(knownSinks, ", "))
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return def, nil
	}
	return out, nil
}

// ParseEnv reads and validates the GOJIBERRY_OBS_* variables that are not specific to one sink. It
// reads no file and starts nothing.
func ParseEnv(getenv func(string) string) (Config, error) {
	get := func(n string) string { return strings.TrimSpace(getenv(n)) }
	var c Config
	switch strings.ToLower(get(envSwitch)) {
	case "", "off":
	case "on":
		c.Enabled = true
	default:
		return c, fmt.Errorf("observe: %s must be \"on\" or \"off\"", envSwitch)
	}
	if !c.Enabled {
		// The kill switch: nothing else is read, so a bad value cannot stop a server that has it off.
		for _, v := range []string{envSinks, envUsageSinks, envTraceSample, envState, "GOJIBERRY_OBS_POSTHOG_KEY", "GOJIBERRY_OBS_POSTHOG_KEY_FILE", "GOJIBERRY_OBS_POSTHOG_HOST"} {
			if get(v) != "" {
				c.Notes = append(c.Notes, v+" is ignored: "+envSwitch+" is not on")
			}
		}
		return c, nil
	}
	var err error
	if c.Sinks, err = parseSinks(envSinks, getenv(envSinks), []string{"builtin"}); err != nil {
		return c, err
	}
	if c.UsageSinks, err = parseSinks(envUsageSinks, getenv(envUsageSinks), nil); err != nil {
		return c, err
	}
	if p := strings.TrimSpace(getenv(envState)); p != "" {
		if slices.Contains(c.Sinks, "builtin") {
			c.StatePath = p
		} else {
			c.Notes = append(c.Notes, envState+" is ignored: the builtin sink is not named in "+envSinks)
		}
	}
	c.TraceSample = DefaultTraceSample
	if v := get(envTraceSample); v != "" {
		f, perr := strconv.ParseFloat(v, 64)
		if perr != nil || f < 0 || f > 1 {
			return c, fmt.Errorf("observe: %s must be a number from 0 to 1", envTraceSample)
		}
		c.TraceSample = f
	}
	return c, nil
}

// FromEnv builds the Observer the environment describes: nil (and no error) when GOJIBERRY_OBS is not
// on, in which case nothing is read, started or sent. It checks the whole configuration at once, so a
// deployment fails at start. The caller closes the Observer at shutdown (Observer.Close flushes).
// getenv is os.Getenv in production.
func FromEnv(ctx context.Context, getenv func(string) string) (*Observer, Config, error) {
	return fromEnv(ctx, getenv, os.ReadFile, ServiceName)
}

// FromEnvFor is FromEnv for another server of the project: service is the name the sinks report it as
// (for example "gojiberry-fontserver").
func FromEnvFor(ctx context.Context, getenv func(string) string, service string) (*Observer, Config, error) {
	return fromEnv(ctx, getenv, os.ReadFile, service)
}

func fromEnv(ctx context.Context, getenv func(string) string, readFile func(string) ([]byte, error), service string) (*Observer, Config, error) {
	c, err := ParseEnv(getenv)
	if err != nil || !c.Enabled {
		return nil, c, err
	}
	env := SinkEnv{Getenv: getenv, ReadFile: readFile, Service: service}
	built := map[string]Sink{}
	closeBuilt := func() {
		for _, s := range built {
			_ = s.Close(ctx)
		}
	}
	for _, n := range append(append([]string(nil), c.Sinks...), c.UsageSinks...) {
		if built[n] != nil {
			continue
		}
		if n == "builtin" {
			b := NewBuiltin()
			if c.StatePath != "" {
				if err := b.KeepIn(c.StatePath); err != nil {
					c.Notes = append(c.Notes, err.Error()+" (the totals start again)")
				}
			}
			built[n] = b
			continue
		}
		f, ok := factory(n)
		if !ok {
			closeBuilt()
			return nil, c, fmt.Errorf("observe: this build does not include the %s sink named in %s or %s", n, envSinks, envUsageSinks)
		}
		s, err := f(ctx, env)
		if err != nil {
			closeBuilt()
			return nil, c, err
		}
		built[n] = s
	}
	pick := func(names []string, skipBuiltin bool) Sink {
		var ss []Sink
		for _, n := range names {
			if skipBuiltin && n == "builtin" {
				continue
			}
			ss = append(ss, built[n])
		}
		if len(ss) == 0 {
			return nil
		}
		return Fanout(ss...)
	}
	o := New(Options{Operator: pick(c.Sinks, false), Usage: pick(c.UsageSinks, true), TraceSample: c.TraceSample})
	o.cfg = &c
	o.named = built
	return o, c, nil
}

// ReadSecret reads a secret that has two forms, NAME with the value and NAME_FILE with the path of a
// file that holds it (the convention of docs/configuration.md; giving both is an error). Errors name
// the variable and never the value.
func ReadSecret(env SinkEnv, name string) (string, error) {
	v := strings.TrimSpace(env.Getenv(name))
	f := strings.TrimSpace(env.Getenv(name + "_FILE"))
	switch {
	case v != "" && f != "":
		return "", fmt.Errorf("observe: set %s or %s_FILE, not both", name, name)
	case f != "":
		read := env.ReadFile
		if read == nil {
			read = os.ReadFile
		}
		b, err := read(f)
		if err != nil {
			return "", fmt.Errorf("observe: cannot read the file named by %s_FILE", name)
		}
		if len(b) > 4096 {
			return "", fmt.Errorf("observe: the file named by %s_FILE is too large for a secret", name)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return v, nil
}
