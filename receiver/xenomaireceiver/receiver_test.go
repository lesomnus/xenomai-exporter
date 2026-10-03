package xenomaireceiver_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lesomnus/xenomai-exporter/metrics"
	"github.com/lesomnus/xenomai-exporter/receiver/xenomaireceiver"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func fixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../xenomai/testdata/rt")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestConfigValidate(t *testing.T) {
	f := xenomaireceiver.NewFactory()
	if err := componenttest.CheckConfigStruct(f.CreateDefaultConfig()); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for desc, tc := range map[string]struct {
		root  string
		valid bool
	}{
		"default":   {"", true},
		"root path": {t.TempDir(), true},
		"relative":  {"hostfs", false},
		"missing":   {t.TempDir() + "/missing", false},
		"file":      {file, false},
	} {
		t.Run(desc, func(t *testing.T) {
			c := f.CreateDefaultConfig().(*xenomaireceiver.Config)
			c.RootPath = tc.root
			err := c.Validate()
			if tc.valid && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func start(t *testing.T, root string) (*consumertest.MetricsSink, *observer.ObservedLogs, func()) {
	t.Helper()
	f := xenomaireceiver.NewFactory()
	c := f.CreateDefaultConfig().(*xenomaireceiver.Config)
	c.RootPath = root
	c.CollectionInterval = 20 * time.Millisecond
	c.InitialDelay = 0
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}

	core, logs := observer.New(zap.InfoLevel)
	set := receivertest.NewNopSettings(xenomaireceiver.Type)
	set.Logger = zap.New(core)

	sink := new(consumertest.MetricsSink)
	r, err := f.CreateMetrics(t.Context(), set, c, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(t.Context(), componenttest.NewNopHost()); err != nil {
		t.Fatal(err)
	}
	return sink, logs, func() {
		if err := r.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReceiver(t *testing.T) {
	sink, _, stop := start(t, fixture(t))
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.AllMetrics()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("want two scrapes")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	all := sink.AllMetrics()
	first := names(all[0])
	second := names(all[1])

	for _, d := range metrics.All {
		switch d {
		case metrics.Faults, metrics.ThreadPeriod:
			continue // none in the fixture
		case metrics.CPUUtilization:
			if _, ok := first[d.Name]; ok {
				t.Error("utilization on the first scrape")
			}
			if _, ok := second[d.Name]; !ok {
				t.Error("no utilization on the second scrape")
			}
			continue
		}
		m, ok := first[d.Name]
		if !ok {
			t.Errorf("%s: missing", d.Name)
			continue
		}
		if m.Unit() != d.Unit || m.Description() != d.Description {
			t.Errorf("%s: unit %q, description %q", d.Name, m.Unit(), m.Description())
		}

		var dps pmetric.NumberDataPointSlice
		if d.Kind == metrics.Counter {
			if m.Type() != pmetric.MetricTypeSum || !m.Sum().IsMonotonic() || m.Sum().AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
				t.Errorf("%s: not a monotonic cumulative sum", d.Name)
				continue
			}
			dps = m.Sum().DataPoints()
		} else {
			if m.Type() != pmetric.MetricTypeGauge {
				t.Errorf("%s: not a gauge", d.Name)
				continue
			}
			dps = m.Gauge().DataPoints()
		}
		for i := range dps.Len() {
			dp := dps.At(i)
			want := pmetric.NumberDataPointValueTypeInt
			if d.Float {
				want = pmetric.NumberDataPointValueTypeDouble
			}
			if dp.ValueType() != want {
				t.Errorf("%s: value type %s", d.Name, dp.ValueType())
			}
			if d.Kind == metrics.Counter && (dp.StartTimestamp() == 0 || dp.StartTimestamp() > dp.Timestamp()) {
				t.Errorf("%s: start %v, time %v", d.Name, dp.StartTimestamp(), dp.Timestamp())
			}
		}
	}

	// Attributes keep their types: cpu is an int.
	dp := first["xenomai.thread.mode_switches"].Sum().DataPoints().At(0)
	if v, ok := dp.Attributes().Get("cpu"); !ok || v.Type() != pcommon.ValueTypeInt {
		t.Errorf("cpu attribute: %v", dp.Attributes().AsRaw())
	}
	// The start of a series stays put across scrapes.
	if a, b := first["xenomai.thread.cpu.time"].Sum().DataPoints().At(0).StartTimestamp(),
		second["xenomai.thread.cpu.time"].Sum().DataPoints().At(0).StartTimestamp(); a != b {
		t.Errorf("start moved: %v -> %v", a, b)
	}
}

func TestNotAvailable(t *testing.T) {
	sink, logs, stop := start(t, t.TempDir())
	time.Sleep(150 * time.Millisecond)
	stop()

	if n := logs.FilterMessageSnippet("not available").Len(); n != 1 {
		t.Errorf("want one idle message, got %d: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zap.WarnLevel).Len() + logs.FilterLevelExact(zap.ErrorLevel).Len(); n != 0 {
		t.Errorf("want no warnings or errors: %v", logs.All())
	}
	if n := sink.DataPointCount(); n != 0 {
		t.Errorf("want no data points, got %d", n)
	}
}

func TestCounterStarts(t *testing.T) {
	d := metrics.ThreadModeSwitches
	a := attribute.NewSet(attribute.String("thread.name", "a"))
	b := attribute.NewSet(attribute.String("thread.name", "b"))
	start := func(md pmetric.Metrics, name string) pcommon.Timestamp {
		dps := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints()
		for i := range dps.Len() {
			if v, _ := dps.At(i).Attributes().Get("thread.name"); v.Str() == name {
				return dps.At(i).StartTimestamp()
			}
		}
		t.Fatalf("no %s", name)
		return 0
	}

	st := xenomaireceiver.NewStarts(100)
	md := xenomaireceiver.ToMetrics([]metrics.Point{{Desc: d, Attrs: a, Value: 5}}, st, 110)
	if s := start(md, "a"); s != 100 {
		t.Errorf("present at the first scrape: start %v, want the receiver's start", s)
	}

	md = xenomaireceiver.ToMetrics([]metrics.Point{{Desc: d, Attrs: a, Value: 9}, {Desc: d, Attrs: b, Value: 1}}, st, 120)
	if s := start(md, "a"); s != 100 {
		t.Errorf("grown: start %v, want unchanged", s)
	}
	if s := start(md, "b"); s != 110 {
		t.Errorf("new: start %v, want the previous scrape", s)
	}

	md = xenomaireceiver.ToMetrics([]metrics.Point{{Desc: d, Attrs: a, Value: 2}, {Desc: d, Attrs: b, Value: 3}}, st, 130)
	if s := start(md, "a"); s != 120 {
		t.Errorf("reset: start %v, want the previous scrape", s)
	}
	if s := start(md, "b"); s != 110 {
		t.Errorf("grown: start %v, want unchanged", s)
	}
}

func names(md pmetric.Metrics) map[string]pmetric.Metric {
	m := map[string]pmetric.Metric{}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := range ms.Len() {
		m[ms.At(i).Name()] = ms.At(i)
	}
	return m
}
