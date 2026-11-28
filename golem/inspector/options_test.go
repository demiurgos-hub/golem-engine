package inspector

import (
	"bytes"
	"log"
	"maps"
	"strings"
	"testing"
)

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv(EnvHubURL, " wss://inspector.example/ws/agent ")
	t.Setenv(EnvToken, " opaque-token ")
	t.Setenv(EnvTargetID, " scene-one ")
	t.Setenv(EnvLabels, "map=caves, region=eu")
	t.Setenv(EnvRateHz, " 2.5 ")
	opts := OptionsFromEnv()
	if opts.HubURL != "wss://inspector.example/ws/agent" || opts.Token != "opaque-token" ||
		opts.TargetID != "scene-one" || opts.RateHz != 2.5 || !maps.Equal(opts.Labels, map[string]string{"map": "caves", "region": "eu"}) {
		t.Fatalf("unexpected Inspector options: %+v", opts)
	}
}

func TestOptionsFromEnvLogsAndIgnoresMalformedValues(t *testing.T) {
	var output bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(prev) })

	t.Setenv(EnvHubURL, "wss://inspector.example/ws/agent?token=secret")
	t.Setenv(EnvToken, "secret\ninvalid")
	t.Setenv(EnvTargetID, strings.Repeat("a", 129))
	t.Setenv(EnvLabels, "region=eu,missing-equals,=no-key")
	t.Setenv(EnvRateHz, "NaN")
	opts := OptionsFromEnv()
	if opts.HubURL != "" || opts.Token != "" || opts.TargetID != "" || opts.RateHz != 0 ||
		!maps.Equal(opts.Labels, map[string]string{"region": "eu"}) {
		t.Fatalf("malformed values were not ignored: %+v", opts)
	}
	for _, name := range []string{EnvHubURL, EnvToken, EnvTargetID, EnvLabels, EnvRateHz} {
		if !strings.Contains(output.String(), "ignoring "+name) {
			t.Errorf("no log for %s", name)
		}
	}
	if strings.Contains(output.String(), "secret") {
		t.Fatal("environment diagnostics logged a credential")
	}
}
