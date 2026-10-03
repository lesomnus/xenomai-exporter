package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lesomnus/otx"
	"github.com/lesomnus/xenomai-exporter/metrics"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collect(t *testing.T, r *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	m := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, v := range sm.Metrics {
			m[v.Name] = v
		}
	}
	return m
}

func setup(t *testing.T, root string) (context.Context, *sdkmetric.ManualReader) {
	t.Helper()
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { mp.Shutdown(context.Background()) })

	ctx := otx.Into(t.Context(), otx.New(otx.WithMeterProvider(mp)))
	if err := register(ctx, &Config{RootPath: root}); err != nil {
		t.Fatal(err)
	}
	return ctx, r
}

func TestRegister(t *testing.T) {
	root, err := filepath.Abs("../xenomai/testdata/rt")
	if err != nil {
		t.Fatal(err)
	}
	_, r := setup(t, root)

	m := collect(t, r)
	for _, d := range metrics.All {
		v, ok := m[d.Name]
		switch d {
		case metrics.Faults, metrics.ThreadPeriod:
			// None in the fixture.
			continue
		case metrics.CPUUtilization:
			if ok {
				t.Errorf("%s: needs two reads", d.Name)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: missing", d.Name)
			continue
		}
		if v.Unit != d.Unit || v.Description != d.Description {
			t.Errorf("%s: unit %q, description %q", d.Name, v.Unit, v.Description)
		}

		switch data := v.Data.(type) {
		case metricdata.Sum[int64]:
			if d.Kind != metrics.Counter || d.Float || !data.IsMonotonic || data.Temporality != metricdata.CumulativeTemporality {
				t.Errorf("%s: int monotonic cumulative sum is not %+v", d.Name, d)
			}
		case metricdata.Sum[float64]:
			if d.Kind != metrics.Counter || !d.Float || !data.IsMonotonic {
				t.Errorf("%s: float sum is not %+v", d.Name, d)
			}
		case metricdata.Gauge[int64]:
			if d.Kind != metrics.Gauge || d.Float {
				t.Errorf("%s: int gauge is not %+v", d.Name, d)
			}
		case metricdata.Gauge[float64]:
			if d.Kind != metrics.Gauge || !d.Float {
				t.Errorf("%s: float gauge is not %+v", d.Name, d)
			}
		default:
			t.Errorf("%s: unexpected %T", d.Name, data)
		}
	}

	sum := m["xenomai.thread.mode_switches"].Data.(metricdata.Sum[int64])
	total := int64(0)
	for _, dp := range sum.DataPoints {
		total += dp.Value
	}
	// Every non-root thread's MSW in sched/acct.
	if want := int64(0 + 0 + 12 + 10 + 1103094 + 1 + 2 + 4 + 42854 + 32945360 + 5); total != want {
		t.Errorf("mode switches: %d, want %d", total, want)
	}

	// A second read has a previous one to compare with.
	m = collect(t, r)
	util, ok := m["xenomai.cpu.utilization"]
	if !ok {
		t.Fatal("utilization missing on the second read")
	}
	// The same files were read twice, so nothing ran in between.
	for _, dp := range util.Data.(metricdata.Gauge[float64]).DataPoints {
		if dp.Value != 0 {
			t.Errorf("utilization of an unchanged snapshot: %v", dp.Value)
		}
	}
}

func TestRegisterNotAvailable(t *testing.T) {
	_, r := setup(t, t.TempDir())
	if m := collect(t, r); len(m) != 0 {
		t.Errorf("want nothing without /proc/xenomai, got %d metrics", len(m))
	}
}

func TestConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte("root_path: /hostfs\nthread_id: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := readConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.RootPath != "/hostfs" || !c.ThreadID || c.Health.Endpoint != ":8081" {
		t.Errorf("%+v", c)
	}

	if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = readConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.RootPath != "" || c.ThreadID || c.Health.Endpoint != ":8081" {
		t.Errorf("defaults: %+v", c)
	}
}
