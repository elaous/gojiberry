package observe

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Env GOJIBERRY_OBS_*; errors name the variable, never repeat a value.
const (
	envSwitch      = "GOJIBERRY_OBS"
	envSinks       = "GOJIBERRY_OBS_SINKS"
	envUsageSinks  = "GOJIBERRY_OBS_USAGE_SINKS"
	envTraceSample = "GOJIBERRY_OBS_TRACE_SAMPLE"
)

const DefaultTraceSample = 0.1

// ServiceName is the name sinks report this server as.
const ServiceName = "gojiberry-docstore"

var knownSinks = []string{"builtin", "otlp", "posthog"}

// Config is the parsed environment.
type Config struct {
	// Enabled is GOJIBERRY_OBS=on.
	Enabled           bool
	Sinks, UsageSinks []string
	TraceSample       float64
	StatePath         string
	Notes             []string
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

func FromEnv(ctx context.Context, getenv func(string) string) (*Observer, Config, error) {
	return fromEnv(ctx, getenv, os.ReadFile, ServiceName)
}

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
