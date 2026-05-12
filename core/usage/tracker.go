package usage

import (
	"sort"
	"strings"
)

// Tracker accumulates usage records by stable subject and measurement keys.
type Tracker struct {
	subjects map[subjectKey]*TrackedSubject
	order    []subjectKey
}

// Snapshot is a point-in-time copy of tracked usage.
type Snapshot struct {
	Subjects []TrackedSubject `json:"subjects,omitempty"`
}

// Empty reports whether the snapshot has no tracked subjects.
func (s Snapshot) Empty() bool {
	return len(s.Subjects) == 0
}

// NewSnapshot returns a snapshot containing the supplied records.
func NewSnapshot(records ...Recorded) Snapshot {
	tracker := NewTracker()
	for _, record := range records {
		tracker.Add(record)
	}
	return tracker.Snapshot()
}

// TrackedSubject contains raw records and accumulated totals for one subject.
type TrackedSubject struct {
	Subject Subject       `json:"subject"`
	Records []Recorded    `json:"records,omitempty"`
	Totals  []Measurement `json:"totals,omitempty"`

	totals map[measurementKey]int
	order  []measurementKey
}

// NewTracker returns an empty usage tracker.
func NewTracker() *Tracker {
	return &Tracker{subjects: map[subjectKey]*TrackedSubject{}}
}

// Add records one usage event.
func (t *Tracker) Add(recorded Recorded) {
	if t == nil || recorded.Empty() {
		return
	}
	if t.subjects == nil {
		t.subjects = map[subjectKey]*TrackedSubject{}
	}
	key := newSubjectKey(recorded.Subject)
	tracked := t.subjects[key]
	if tracked == nil {
		subject := recorded.Subject
		subject.ID = ""
		tracked = &TrackedSubject{
			Subject: subject,
			totals:  map[measurementKey]int{},
		}
		t.subjects[key] = tracked
		t.order = append(t.order, key)
	}
	tracked.Records = append(tracked.Records, cloneRecorded(recorded))
	for _, measurement := range recorded.Measurements {
		if measurement.Quantity == 0 {
			continue
		}
		mkey := newMeasurementKey(measurement)
		index, ok := tracked.totals[mkey]
		if !ok {
			index = len(tracked.Totals)
			tracked.totals[mkey] = index
			tracked.order = append(tracked.order, mkey)
			total := cloneMeasurement(measurement)
			total.Quantity = 0
			tracked.Totals = append(tracked.Totals, total)
		}
		tracked.Totals[index].Quantity += measurement.Quantity
	}
}

// Snapshot returns a detached copy of accumulated usage.
func (t *Tracker) Snapshot() Snapshot {
	if t == nil || len(t.order) == 0 {
		return Snapshot{}
	}
	out := Snapshot{Subjects: make([]TrackedSubject, 0, len(t.order))}
	for _, key := range t.order {
		tracked := t.subjects[key]
		if tracked == nil {
			continue
		}
		copySubject := TrackedSubject{
			Subject: cloneSubject(tracked.Subject),
			Records: make([]Recorded, len(tracked.Records)),
			Totals:  make([]Measurement, len(tracked.Totals)),
		}
		for i, record := range tracked.Records {
			copySubject.Records[i] = cloneRecorded(record)
		}
		for i, measurement := range tracked.Totals {
			copySubject.Totals[i] = cloneMeasurement(measurement)
		}
		out.Subjects = append(out.Subjects, copySubject)
	}
	return out
}

// Reset clears all accumulated usage.
func (t *Tracker) Reset() {
	if t == nil {
		return
	}
	t.subjects = map[subjectKey]*TrackedSubject{}
	t.order = nil
}

type subjectKey struct {
	kind     SubjectKind
	provider string
	name     string
}

func newSubjectKey(subject Subject) subjectKey {
	return subjectKey{
		kind:     subject.Kind,
		provider: strings.TrimSpace(subject.Provider),
		name:     strings.TrimSpace(subject.Name),
	}
}

type measurementKey struct {
	metric     MetricName
	unit       Unit
	direction  Direction
	dimensions string
}

func newMeasurementKey(measurement Measurement) measurementKey {
	return measurementKey{
		metric:     measurement.Metric,
		unit:       measurement.Unit,
		direction:  measurement.Direction,
		dimensions: dimensionsKey(measurement.Dimensions),
	}
}

func dimensionsKey(dimensions map[string]string) string {
	if len(dimensions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+dimensions[key])
	}
	return strings.Join(parts, "\x00")
}

func cloneRecorded(recorded Recorded) Recorded {
	return Recorded{
		Source:       recorded.Source,
		Subject:      cloneSubject(recorded.Subject),
		Measurements: cloneMeasurements(recorded.Measurements),
	}
}

func cloneSubject(subject Subject) Subject {
	out := subject
	if subject.Attributes != nil {
		out.Attributes = map[string]string{}
		for key, value := range subject.Attributes {
			out.Attributes[key] = value
		}
	}
	return out
}

func cloneMeasurements(measurements []Measurement) []Measurement {
	if len(measurements) == 0 {
		return nil
	}
	out := make([]Measurement, len(measurements))
	for i, measurement := range measurements {
		out[i] = cloneMeasurement(measurement)
	}
	return out
}

func cloneMeasurement(measurement Measurement) Measurement {
	out := measurement
	if measurement.Dimensions != nil {
		out.Dimensions = map[string]string{}
		for key, value := range measurement.Dimensions {
			out.Dimensions[key] = value
		}
	}
	return out
}
