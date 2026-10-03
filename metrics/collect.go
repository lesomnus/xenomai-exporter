package metrics

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/lesomnus/xenomai-exporter/xenomai"
	"go.opentelemetry.io/otel/attribute"
)

type Point struct {
	Desc  *Desc
	Attrs attribute.Set
	Value float64
}

type Options struct {
	// Adds `thread.id` to thread metrics, so threads of the same name, CPU, class, and priority are reported apart.
	// It changes whenever a program restarts, so every restart starts new series.
	ThreadID bool
}

// Collector turns snapshots into points.
// It keeps the previous snapshot's execution times to report CPU utilization, so use one per reader.
type Collector struct {
	opts Options

	mu   sync.Mutex
	prev *window
}

type threadKey struct {
	pid  int
	name string
}

type window struct {
	exec  map[threadKey]time.Duration
	ticks uint64
	time  time.Time
}

func New(opts Options) *Collector {
	return &Collector{opts: opts}
}

var privateHeap = regexp.MustCompile(`^(.*\S)\s*\[(\d+)\]$`)

// Collect returns the points of a snapshot, in the order of All.
// Points with the same attributes are merged; see Desc.Max.
func (c *Collector) Collect(s *xenomai.Snapshot) []Point {
	c.mu.Lock()
	defer c.mu.Unlock()

	b := newBuilder()

	b.add(Info, 1,
		attribute.String(AttrVersion, s.Version),
		attribute.String(AttrAffinity, s.Affinity),
		attribute.String(AttrClockTimer, s.Clock.Timer),
		attribute.String(AttrClockSource, s.Clock.Source),
		attribute.String(AttrClockWatchdog, s.Clock.Watchdog),
	)
	for _, g := range []struct {
		ctx string
		d   time.Duration
	}{
		{"irq", s.Clock.Gravity.IRQ},
		{"kernel", s.Clock.Gravity.Kernel},
		{"user", s.Clock.Gravity.User},
	} {
		b.add(ClockGravity, g.d.Seconds(), attribute.String(AttrClockContext, g.ctx))
	}

	for _, f := range s.Faults {
		if f.Count == 0 {
			continue
		}
		b.add(Faults, float64(f.Count),
			attribute.Int(AttrCPU, f.CPU),
			attribute.Int(AttrFaultTrap, f.Trap),
			attribute.String(AttrFaultType, f.Label),
		)
	}

	names := map[int]string{}
	for _, t := range s.Threads {
		if t.PID != 0 {
			names[t.PID] = t.Name
		}
	}
	for _, h := range s.Heaps {
		attrs := []attribute.KeyValue{attribute.String(AttrHeapName, h.Name)}
		if m := privateHeap.FindStringSubmatch(h.Name); m != nil {
			// "private heap[3510]": name the owner rather than its PID, which changes on every restart.
			pid, _ := strconv.Atoi(m[2])
			owner, ok := names[pid]
			if !ok {
				owner = m[2]
			}
			attrs = []attribute.KeyValue{
				attribute.String(AttrHeapName, m[1]),
				attribute.String(AttrHeapOwner, owner),
			}
			if c.opts.ThreadID {
				attrs = append(attrs, attribute.Int(AttrThreadID, pid))
			}
		}
		b.add(HeapSize, float64(h.Total), attrs...)
		b.add(HeapUsage, float64(h.Total-h.Free), attrs...)
	}

	if s.Registry.Slots > 0 {
		b.add(RegistryUsage, float64(s.Registry.Used))
		b.add(RegistryLimit, float64(s.Registry.Slots))
	}

	cur := &window{
		exec:  map[threadKey]time.Duration{},
		ticks: s.Clock.Ticks,
		time:  s.Time,
	}
	busy := map[int]time.Duration{}
	for _, t := range s.Threads {
		if t.State.Has(xenomai.StateRoot) {
			// The root thread of a CPU stands for Linux; its accounting only advances on
			// switches, so it is not a measure of anything. It marks a real-time CPU.
			busy[t.CPU] += 0
			continue
		}

		key := threadKey{t.PID, t.Name}
		cur.exec[key] = t.ExecTime
		if c.prev != nil {
			// A thread new since the previous read ran all of its time in between.
			d := t.ExecTime - c.prev.exec[key]
			if d > 0 {
				busy[t.CPU] += d
			}
		}

		attrs := []attribute.KeyValue{
			attribute.String(AttrThreadName, t.Name),
			attribute.Int(AttrCPU, t.CPU),
			attribute.String(AttrThreadClass, t.Class),
			attribute.Int(AttrThreadPrio, t.Priority),
		}
		if c.opts.ThreadID {
			attrs = append(attrs, attribute.Int(AttrThreadID, t.PID))
		}
		relaxed := 0.0
		if t.State.Has(xenomai.StateRelax) {
			relaxed = 1
		}
		b.add(ThreadCount, 1, attrs...)
		b.add(ThreadRelaxed, relaxed, attrs...)
		b.add(ThreadModeSwitches, float64(t.ModeSwitches), attrs...)
		b.add(ThreadContextSwitches, float64(t.ContextSwitches), attrs...)
		b.add(ThreadSyscalls, float64(t.Syscalls), attrs...)
		b.add(ThreadPageFaults, float64(t.PageFaults), attrs...)
		b.add(ThreadCPUTime, t.ExecTime.Seconds(), attrs...)
		if t.Period > 0 {
			b.add(ThreadPeriod, t.Period.Seconds(), attrs...)
		}
	}

	if c.prev != nil {
		if elapsed := elapsedBetween(c.prev, cur); elapsed > 0 {
			cpus := slices.Sorted(maps.Keys(busy))
			for _, cpu := range cpus {
				b.add(CPUUtilization, min(1, float64(busy[cpu])/float64(elapsed)), attribute.Int(AttrCPU, cpu))
			}
		}
	}
	c.prev = cur

	return b.points()
}

// elapsedBetween prefers the core clock, which execution times are measured on.
func elapsedBetween(prev, cur *window) time.Duration {
	if prev.ticks > 0 && cur.ticks > prev.ticks {
		return time.Duration(cur.ticks - prev.ticks)
	}
	return cur.time.Sub(prev.time)
}

type builder struct {
	index  map[*Desc]map[attribute.Distinct]int
	values []Point
}

func newBuilder() *builder {
	return &builder{index: map[*Desc]map[attribute.Distinct]int{}}
}

func (b *builder) add(d *Desc, v float64, kvs ...attribute.KeyValue) {
	set := attribute.NewSet(kvs...)
	idx, ok := b.index[d]
	if !ok {
		idx = map[attribute.Distinct]int{}
		b.index[d] = idx
	}
	if i, ok := idx[set.Equivalent()]; ok {
		p := &b.values[i]
		if d.Max {
			p.Value = max(p.Value, v)
		} else {
			p.Value += v
		}
		return
	}
	idx[set.Equivalent()] = len(b.values)
	b.values = append(b.values, Point{Desc: d, Attrs: set, Value: v})
}

func (b *builder) points() []Point {
	order := map[*Desc]int{}
	for i, d := range All {
		order[d] = i
	}
	slices.SortStableFunc(b.values, func(x, y Point) int {
		return order[x.Desc] - order[y.Desc]
	})
	return b.values
}
