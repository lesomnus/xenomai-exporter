package xenomaireceiver

import (
	"context"
	"errors"
	"time"

	"github.com/lesomnus/xenomai-exporter/metrics"
	"github.com/lesomnus/xenomai-exporter/xenomai"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type xenomaiScraper struct {
	root      string
	collector *metrics.Collector
	logger    *zap.Logger

	unavailable bool
	starts      *starts
}

func newScraper(c *Config, logger *zap.Logger) *xenomaiScraper {
	return &xenomaiScraper{
		root:      c.RootPath,
		collector: metrics.New(metrics.Options{ThreadID: c.ThreadID}),
		logger:    logger,
	}
}

func (s *xenomaiScraper) start(_ context.Context, _ component.Host) error {
	s.starts = newStarts(pcommon.NewTimestampFromTime(time.Now()))
	return nil
}

func (s *xenomaiScraper) scrape(_ context.Context) (pmetric.Metrics, error) {
	snap, err := xenomai.Read(s.root)
	if err != nil {
		if errors.Is(err, xenomai.ErrNotAvailable) {
			// One config fits every node: where there is no Cobalt core, say so once and stay idle.
			if !s.unavailable {
				s.logger.Info("xenomai is not available; the receiver stays idle", zap.String("dir", xenomai.Dir(s.root)))
				s.unavailable = true
			}
			return pmetric.NewMetrics(), nil
		}
		return pmetric.NewMetrics(), err
	}
	if s.unavailable {
		s.logger.Info("xenomai is available", zap.String("dir", xenomai.Dir(s.root)))
		s.unavailable = false
	}

	ts := pcommon.NewTimestampFromTime(snap.Time)
	return toMetrics(s.collector.Collect(snap), s.starts, ts), nil
}

// starts keeps when each counter series started: when the receiver did, or for a series
// that appeared or reset later, at the previous scrape.
type starts struct {
	first pcommon.Timestamp
	prev  pcommon.Timestamp
	seen  map[*metrics.Desc]map[attribute.Distinct]series
}

type series struct {
	start pcommon.Timestamp
	last  float64
}

func newStarts(first pcommon.Timestamp) *starts {
	return &starts{first: first, prev: first, seen: map[*metrics.Desc]map[attribute.Distinct]series{}}
}

func toMetrics(points []metrics.Point, st *starts, ts pcommon.Timestamp) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(ScopeName)

	seen := map[*metrics.Desc]map[attribute.Distinct]series{}
	var (
		desc *metrics.Desc
		dps  pmetric.NumberDataPointSlice
	)
	for _, p := range points {
		if p.Desc != desc {
			desc = p.Desc
			m := sm.Metrics().AppendEmpty()
			m.SetName(desc.Name)
			m.SetDescription(desc.Description)
			m.SetUnit(desc.Unit)
			if desc.Kind == metrics.Counter {
				sum := m.SetEmptySum()
				sum.SetIsMonotonic(true)
				sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				dps = sum.DataPoints()
			} else {
				dps = m.SetEmptyGauge().DataPoints()
			}
		}

		dp := dps.AppendEmpty()
		dp.SetTimestamp(ts)
		if desc.Float {
			dp.SetDoubleValue(p.Value)
		} else {
			dp.SetIntValue(int64(p.Value))
		}
		for _, kv := range p.Attrs.ToSlice() {
			switch kv.Value.Type() {
			case attribute.INT64:
				dp.Attributes().PutInt(string(kv.Key), kv.Value.AsInt64())
			default:
				dp.Attributes().PutStr(string(kv.Key), kv.Value.Emit())
			}
		}

		if desc.Kind == metrics.Counter && st != nil {
			key := p.Attrs.Equivalent()
			s, ok := st.seen[desc][key]
			switch {
			case !ok && st.prev == st.first:
				s.start = st.first
			case !ok || p.Value < s.last:
				// New since the previous scrape, or reset, e.g. a thread restarted.
				s.start = st.prev
			}
			s.last = p.Value
			if seen[desc] == nil {
				seen[desc] = map[attribute.Distinct]series{}
			}
			seen[desc][key] = s
			dp.SetStartTimestamp(s.start)
		}
	}

	if st != nil {
		// Forget series that went away, so their keys do not pile up.
		st.seen = seen
		st.prev = ts
	}
	return md
}
