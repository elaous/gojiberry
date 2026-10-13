package observe

import (
	"os"
	"strings"
	"testing"
)

// TestDocsListEverythingTheAllowlistAllows keeps docs/observability.md in step with the code: a
// metric, label value, span, attribute or variable that is not documented fails here.
func TestDocsListEverythingTheAllowlistAllows(t *testing.T) {
	b, err := os.ReadFile("../docs/observability.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	need := func(kind, s string) {
		if !strings.Contains(doc, "`"+s+"`") {
			t.Errorf("docs/observability.md does not mention the %s `%s`", kind, s)
		}
	}
	for _, d := range defs {
		need("metric", string(d.Name))
		for _, l := range d.Labels {
			need("label", l)
		}
	}
	for l, vs := range labelValues {
		for _, v := range vs {
			need("value of "+l, v)
		}
	}
	for _, s := range spanDefs {
		need("span", string(s.Name))
		for _, a := range s.Attrs {
			need("attribute", a)
		}
	}
	for _, v := range []string{envSwitch, envSinks, envUsageSinks, envTraceSample, envPostHogHost, envPostHogKey, envPostHogID, envPostHogInterval} {
		need("variable", v)
	}
	for _, n := range NeverSent {
		if n == "" {
			t.Error("empty never-sent entry")
		}
	}
}

// TestConfigurationDocNamesTheVariables keeps docs/configuration.md pointing at the new variables.
func TestConfigurationDocNamesTheVariables(t *testing.T) {
	b, err := os.ReadFile("../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{envSwitch, envSinks, envUsageSinks, envTraceSample, envPostHogHost, envPostHogKey, envPostHogID, envPostHogInterval} {
		if !strings.Contains(string(b), v) {
			t.Errorf("docs/configuration.md does not mention %s", v)
		}
	}
}
