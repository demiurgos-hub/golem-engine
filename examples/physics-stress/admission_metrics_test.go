package main

import (
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

func TestAdmissionMetricsExcludeSetupAndUnknownAutomaticLatency(t *testing.T) {
	start := time.Unix(1000, 0)
	a := &actor{ID: 1, requestAt: start.Add(-time.Minute), requestGeneration: 1}
	s := &scene{actors: []*actor{a}}
	s.beginAdmissionMeasurement(start)
	s.observePathAdmission(golem.NavigationPathAdmissionMetrics{EntityID: 1, Generation: 1, AdmittedAt: start.Add(200 * time.Millisecond)})
	if s.admissionLatency.count != 1 || s.admissionLatency.maximum != 200*time.Millisecond || !a.admitted {
		t.Fatalf("startup included setup or missed admission: %+v", s.admissionLatency)
	}
	if !a.requestAt.IsZero() || a.requestGeneration != 0 {
		t.Fatal("admission retained an obsolete request timestamp")
	}
	s.observePathAdmission(golem.NavigationPathAdmissionMetrics{EntityID: 1, Generation: 2, AdmittedAt: start.Add(time.Minute)})
	if s.automaticAdmissions != 1 || s.renewalLatency.count != 0 {
		t.Fatal("automatic replan invented a measurable request duration")
	}

	a.requestAt, a.requestGeneration = start.Add(2*time.Minute), 3
	s.observePathAdmission(golem.NavigationPathAdmissionMetrics{EntityID: 1, Generation: 3, AdmittedAt: a.requestAt.Add(150 * time.Millisecond)})
	if s.renewalLatency.count != 1 || s.renewalLatency.maximum != 150*time.Millisecond {
		t.Fatal("matching explicit destination request lost its measured latency")
	}
	a.requestAt, a.requestGeneration = start.Add(3*time.Minute), 4
	s.observePathAdmission(golem.NavigationPathAdmissionMetrics{EntityID: 1, Generation: 5, AdmittedAt: start.Add(4 * time.Minute)})
	s.observePathAdmission(golem.NavigationPathAdmissionMetrics{EntityID: 1, Generation: 6, AdmittedAt: start.Add(5 * time.Minute)})
	if s.automaticAdmissions != 3 || s.renewalLatency.count != 1 || !a.requestAt.IsZero() || a.requestGeneration != 0 {
		t.Fatal("superseded explicit request was reused for automatic admission")
	}
}

func TestAdmissionWithoutKnownStartupIsCountedWithoutDuration(t *testing.T) {
	s := &scene{actors: []*actor{{ID: 1}}}
	s.observePathAdmission(golem.NavigationPathAdmissionMetrics{EntityID: 1, Generation: 1, AdmittedAt: time.Unix(1000, 0)})
	if s.unmeasuredStartupAdmissions != 1 || s.admissionLatency.count != 0 || !s.actors[0].admitted {
		t.Fatal("unknown startup boundary was reported as a zero-duration sample")
	}
}
