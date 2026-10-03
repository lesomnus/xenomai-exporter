package metrics_test

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lesomnus/xenomai-exporter/metrics"
	"github.com/lesomnus/xenomai-exporter/xenomai"
)

func read(t *testing.T, root string) *xenomai.Snapshot {
	t.Helper()
	s, err := xenomai.Read("../xenomai/testdata/" + root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// index points by metric name and then by attributes, rendered as "k=v,k=v" in key order.
func index(points []metrics.Point) map[string]map[string]float64 {
	m := map[string]map[string]float64{}
	for _, p := range points {
		if m[p.Desc.Name] == nil {
			m[p.Desc.Name] = map[string]float64{}
		}
		kvs := []string{}
		for _, kv := range p.Attrs.ToSlice() {
			kvs = append(kvs, string(kv.Key)+"="+kv.Value.Emit())
		}
		key := strings.Join(kvs, ",")
		if _, dup := m[p.Desc.Name][key]; dup {
			panic("duplicated point: " + p.Desc.Name + "{" + key + "}")
		}
		m[p.Desc.Name][key] = p.Value
	}
	return m
}

func threadKey(name string, cpu, prio int) string {
	return "cpu=" + strconv.Itoa(cpu) + ",thread.name=" + name + ",xenomai.thread.class=rt,xenomai.thread.priority=" + strconv.Itoa(prio)
}

func TestCollect(t *testing.T) {
	c := metrics.New(metrics.Options{})
	m := index(c.Collect(read(t, "rt")))

	if v := m["xenomai.info"]; len(v) != 1 || v["xenomai.affinity=0000ffff,xenomai.clock.source=<Linux clocksource>,xenomai.clock.timer=lapic-deadline,xenomai.clock.watchdog=off,xenomai.version=3.3.3"] != 1 {
		t.Errorf("info: %v", v)
	}
	if v := m["xenomai.clock.gravity"]; v["xenomai.clock.context=irq"] != 0 || v["xenomai.clock.context=kernel"] != 3350e-9 || v["xenomai.clock.context=user"] != 3350e-9 {
		t.Errorf("gravity: %v", v)
	}
	if v, ok := m["xenomai.faults"]; ok {
		t.Errorf("faults are all zero, so none are reported: %v", v)
	}
	if v := m["xenomai.registry.usage"]; v[""] != 111 {
		t.Errorf("registry usage: %v", v)
	}
	if v := m["xenomai.registry.limit"]; v[""] != 512 {
		t.Errorf("registry limit: %v", v)
	}

	// Private heaps are named after the thread owning them, not its PID.
	want_heaps := map[string]float64{
		"xenomai.heap.name=system heap":                                     4194304 - 4134848,
		"xenomai.heap.name=shared heap":                                     262144 - 262000,
		"xenomai.heap.name=private heap,xenomai.heap.owner=controller_rt":   262144 - 261712,
		"xenomai.heap.name=private heap,xenomai.heap.owner=friday_ros2":     262144 - 261680,
		"xenomai.heap.name=private heap,xenomai.heap.owner=ethercat_master": 262144 - 261568,
	}
	if got := m["xenomai.heap.usage"]; !equal(got, want_heaps) {
		t.Errorf("heap usage:\n got %v\nwant %v", got, want_heaps)
	}
	if got := m["xenomai.heap.size"]; len(got) != 5 || got["xenomai.heap.name=system heap"] != 4194304 {
		t.Errorf("heap size: %v", got)
	}

	// Root threads are not threads to report; 11 others in 8 groups.
	count := m["xenomai.thread.count"]
	if len(count) != 8 {
		t.Errorf("thread groups: %d: %v", len(count), count)
	}
	for _, d := range []string{
		"xenomai.thread.count",
		"xenomai.thread.relaxed",
		"xenomai.thread.mode_switches",
		"xenomai.thread.context_switches",
		"xenomai.thread.syscalls",
		"xenomai.thread.page_faults",
		"xenomai.thread.cpu.time",
	} {
		if len(m[d]) != len(count) {
			t.Errorf("%s: %d points, want one per group", d, len(m[d]))
		}
	}

	// The four ethercat_master threads share a name, CPU, class, and priority: summed.
	em := threadKey("ethercat_master", 5, 0)
	if v := count[em]; v != 4 {
		t.Errorf("ethercat_master threads: %v", v)
	}
	if v := m["xenomai.thread.mode_switches"][em]; v != 1103094+1+42854+32945360 {
		t.Errorf("ethercat_master mode switches: %v", v)
	}
	if v := m["xenomai.thread.cpu.time"][em]; !near(v, (2225977295+1172+110268622+131917580643)/1e9, 1e-9) {
		t.Errorf("ethercat_master cpu time: %v", v)
	}

	rt := threadKey("hday_ecat_rt", 6, 85)
	for name, want := range map[string]float64{
		"xenomai.thread.count":            1,
		"xenomai.thread.relaxed":          0,
		"xenomai.thread.mode_switches":    4,
		"xenomai.thread.context_switches": 13825103,
		"xenomai.thread.syscalls":         1254507687,
		"xenomai.thread.page_faults":      0,
		"xenomai.thread.cpu.time":         1591.327084202,
	} {
		if v, ok := m[name][rt]; !ok || !near(v, want, 1e-9) {
			t.Errorf("hday_ecat_rt %s: %v, want %v", name, v, want)
		}
	}
	if v := m["xenomai.thread.relaxed"][threadKey("controller_rt", 2, 75)]; v != 1 {
		t.Errorf("controller_rt is relaxed: %v", v)
	}
	if _, ok := m["xenomai.thread.period"]; ok {
		t.Error("no thread is periodic")
	}

	if _, ok := m["xenomai.cpu.utilization"]; ok {
		t.Error("utilization needs two reads")
	}
}

// The second snapshot's sched/stat was read first, so its %CPU covers the window
// since the first snapshot: Xenomai's own measure of what utilization should be.
func TestUtilizationMatchesSchedStat(t *testing.T) {
	c := metrics.New(metrics.Options{})
	c.Collect(read(t, "rt"))
	got := index(c.Collect(read(t, "rt-later")))["xenomai.cpu.utilization"]

	want := map[int]float64{}
	for cpu := range 16 {
		want[cpu] = 0
	}
	f, err := os.Open("../xenomai/testdata/rt-later/proc/xenomai/sched/stat")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	for sc.Scan() {
		c := strings.Fields(sc.Text())
		if strings.HasPrefix(c[8], "[ROOT/") {
			continue
		}
		cpu, _ := strconv.Atoi(c[0])
		pct, _ := strconv.ParseFloat(c[7], 64)
		want[cpu] += pct / 100
	}

	if len(got) != len(want) {
		t.Fatalf("utilization for %d CPUs, want %d: %v", len(got), len(want), got)
	}
	for cpu, w := range want {
		g, ok := got["cpu="+strconv.Itoa(cpu)]
		if !ok {
			t.Errorf("cpu %d: missing", cpu)
			continue
		}
		// %CPU is rounded to a tenth of a percent per thread, and the windows differ by the
		// moments between reading sched/stat and sched/acct.
		if !near(g, w, 0.003) {
			t.Errorf("cpu %d: %.4f, sched/stat says %.4f", cpu, g, w)
		}
	}
	if got["cpu=6"] < 0.3 || got["cpu=8"] < 0.3 {
		t.Errorf("the control loops on CPU 6 and 8 run about a third of the time: %v", got)
	}
}

func TestThreadID(t *testing.T) {
	c := metrics.New(metrics.Options{ThreadID: true})
	m := index(c.Collect(read(t, "rt")))

	if n := len(m["xenomai.thread.count"]); n != 11 {
		t.Errorf("with thread IDs every thread is apart: %d", n)
	}
	key := "cpu=5,thread.id=3745,thread.name=ethercat_master,xenomai.thread.class=rt,xenomai.thread.priority=0"
	if v := m["xenomai.thread.mode_switches"][key]; v != 32945360 {
		t.Errorf("ethercat_master 3745: %v", v)
	}
	if _, ok := m["xenomai.heap.usage"]["thread.id=3510,xenomai.heap.name=private heap,xenomai.heap.owner=controller_rt"]; !ok {
		t.Errorf("private heap with thread id: %v", m["xenomai.heap.usage"])
	}
}

func TestMerge(t *testing.T) {
	s := &xenomai.Snapshot{
		Version: "3.3.3",
		Faults: []xenomai.Fault{
			{CPU: 1, Trap: 14, Label: "Page fault", Count: 3},
			{CPU: 1, Trap: 19, Label: "SIMD error"},
		},
		Heaps: []xenomai.Heap{{Name: "private heap[999]", Total: 10, Free: 4}},
		Threads: []xenomai.Thread{
			{CPU: 0, Name: "ROOT/0", Class: "idle", Priority: -1, State: xenomai.StateRoot},
			{CPU: 0, PID: 10, Name: "loop", Class: "rt", Priority: 90, Period: time.Millisecond},
			{CPU: 0, PID: 11, Name: "loop", Class: "rt", Priority: 90, Period: 2 * time.Millisecond, State: xenomai.StateRelax},
		},
	}
	m := index(metrics.New(metrics.Options{}).Collect(s))

	key := threadKey("loop", 0, 90)
	if v := m["xenomai.thread.period"][key]; v != 0.002 {
		t.Errorf("period takes the maximum: %v", v)
	}
	if v := m["xenomai.thread.relaxed"][key]; v != 1 {
		t.Errorf("relaxed sums: %v", v)
	}
	if v := m["xenomai.faults"]; len(v) != 1 || v["cpu=1,xenomai.fault.trap=14,xenomai.fault.type=Page fault"] != 3 {
		t.Errorf("only non-zero faults: %v", v)
	}
	// No thread has PID 999: the PID stands in for the owner.
	if v := m["xenomai.heap.usage"]["xenomai.heap.name=private heap,xenomai.heap.owner=999"]; v != 6 {
		t.Errorf("heap of an unknown owner: %v", m["xenomai.heap.usage"])
	}
	if _, ok := m["xenomai.registry.usage"]; ok {
		t.Error("no registry without its file")
	}
}

func TestUtilizationNewThread(t *testing.T) {
	first := &xenomai.Snapshot{
		Clock:   xenomai.Clock{Ticks: 1_000_000_000},
		Threads: []xenomai.Thread{{CPU: 0, Name: "ROOT/0", State: xenomai.StateRoot}},
	}
	second := &xenomai.Snapshot{
		Clock: xenomai.Clock{Ticks: 3_000_000_000},
		Threads: []xenomai.Thread{
			{CPU: 0, Name: "ROOT/0", State: xenomai.StateRoot},
			// Started in between: all of its time is in the window.
			{CPU: 0, PID: 7, Name: "new", ExecTime: 500 * time.Millisecond},
		},
	}
	c := metrics.New(metrics.Options{})
	c.Collect(first)
	got := index(c.Collect(second))["xenomai.cpu.utilization"]
	if v := got["cpu=0"]; v != 0.25 {
		t.Errorf("0.5s of 2s: %v", got)
	}
}

func TestOrder(t *testing.T) {
	c := metrics.New(metrics.Options{})
	c.Collect(read(t, "rt"))
	points := c.Collect(read(t, "rt-later"))

	order := map[*metrics.Desc]int{}
	for i, d := range metrics.All {
		order[d] = i
	}
	for i := 1; i < len(points); i++ {
		if order[points[i-1].Desc] > order[points[i].Desc] {
			t.Fatalf("%s before %s", points[i-1].Desc.Name, points[i].Desc.Name)
		}
	}

	seen := map[*metrics.Desc]bool{}
	for _, p := range points {
		seen[p.Desc] = true
	}
	for _, d := range metrics.All {
		if !seen[d] && d != metrics.Faults && d != metrics.ThreadPeriod {
			t.Errorf("%s not reported", d.Name)
		}
	}
}

func near(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func equal(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
