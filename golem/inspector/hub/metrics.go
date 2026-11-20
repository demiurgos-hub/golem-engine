package hub

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const metricPrefix = "golem_inspector_"

// metrics is a minimal Prometheus text-format registry. Label sets are
// bounded by construction (fixed reasons, roles and message types).
type metrics struct {
	mu       sync.Mutex
	counters map[string]map[string]float64
	help     map[string]string
}

var metricHelp = map[string]string{
	"connections_total":               "WebSocket connection attempts by role and outcome.",
	"closes_total":                    "WebSocket closes by role and reason.",
	"messages_total":                  "Inbound protocol messages by role and type.",
	"rejections_total":                "Rejected requests by surface and reason.",
	"observe_commands_total":          "Observation commands sent to agents.",
	"stale_cleanups_total":            "Targets purged after missing heartbeats.",
	"resync_total":                    "Resyncs requested from browsers by reason.",
	"snapshots_total":                 "Agent snapshots by result.",
	"snapshot_duration_seconds_sum":   "Total seconds spent receiving committed snapshots.",
	"snapshot_duration_seconds_count": "Committed snapshots counted in the duration sum.",
}

func newMetrics() *metrics {
	return &metrics{counters: map[string]map[string]float64{}}
}

func labelKey(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		parts = append(parts, fmt.Sprintf("%s=%q", labels[i], labels[i+1]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (m *metrics) add(name string, v float64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	series := m.counters[name]
	if series == nil {
		series = map[string]float64{}
		m.counters[name] = series
	}
	series[labelKey(labels)] += v
}

func (m *metrics) inc(name string, labels ...string) { m.add(name, 1, labels...) }

type gauge struct {
	name, help string
	series     map[string]float64
}

func (m *metrics) write(w http.ResponseWriter, gauges []gauge) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.counters))
	for n := range m.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, g := range gauges {
		writeSeries(w, g.name, g.help, "gauge", g.series)
	}
	for _, n := range names {
		writeSeries(w, n, metricHelp[n], "counter", m.counters[n])
	}
}

func writeSeries(w http.ResponseWriter, name, help, typ string, series map[string]float64) {
	full := metricPrefix + name
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", full, help, full, typ)
	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "%s%s %g\n", full, k, series[k])
	}
}
