package xenomai_test

import (
	"bufio"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lesomnus/xenomai-exporter/xenomai"
)

func TestReadRT(t *testing.T) {
	s, err := xenomai.Read("testdata/rt")
	if err != nil {
		t.Fatal(err)
	}

	if s.Version != "3.3.3" {
		t.Errorf("version: %q", s.Version)
	}
	if s.Affinity != "0000ffff" {
		t.Errorf("affinity: %q", s.Affinity)
	}
	want_clock := xenomai.Clock{
		Gravity:  xenomai.Gravity{IRQ: 0, Kernel: 3350, User: 3350},
		Timer:    "lapic-deadline",
		Source:   "<Linux clocksource>",
		Watchdog: "off",
		Ticks:    4375800710602,
	}
	if s.Clock != want_clock {
		t.Errorf("clock:\n got %+v\nwant %+v", s.Clock, want_clock)
	}
	if s.Registry != (xenomai.Registry{Used: 111, Slots: 512}) {
		t.Errorf("registry: %+v", s.Registry)
	}

	// 19 traps for each of 16 CPUs, none taken.
	if len(s.Faults) != 19*16 {
		t.Errorf("faults: %d", len(s.Faults))
	}
	for _, f := range s.Faults {
		if f.Count != 0 || f.Label == "" {
			t.Errorf("fault: %+v", f)
		}
	}
	found := false
	for _, f := range s.Faults {
		if f.Trap == 14 && f.CPU == 3 {
			found = f == (xenomai.Fault{CPU: 3, Trap: 14, Label: "Page fault"})
		}
	}
	if !found {
		t.Error("no page fault entry for CPU3")
	}

	want_heaps := []xenomai.Heap{
		{Name: "system heap", Total: 4194304, Free: 4134848},
		{Name: "shared heap", Total: 262144, Free: 262000},
		{Name: "private heap[3510]", Total: 262144, Free: 261712},
		{Name: "private heap[3512]", Total: 262144, Free: 261680},
		{Name: "private heap[3492]", Total: 262144, Free: 261568},
	}
	if !reflect.DeepEqual(s.Heaps, want_heaps) {
		t.Errorf("heaps:\n got %+v\nwant %+v", s.Heaps, want_heaps)
	}

	if len(s.Threads) != 27 {
		t.Fatalf("threads: %d", len(s.Threads))
	}
	roots := 0
	for _, th := range s.Threads {
		if th.State.Has(xenomai.StateRoot) {
			roots++
			if th.Name != "ROOT/"+strconv.Itoa(th.CPU) || th.Class != "idle" || th.Priority != -1 {
				t.Errorf("root thread: %+v", th)
			}
		}
	}
	if roots != 16 {
		t.Errorf("root threads: %d", roots)
	}

	want := xenomai.Thread{
		CPU:             6,
		PID:             3743,
		ModeSwitches:    4,
		ContextSwitches: 13825103,
		Syscalls:        1254507687,
		PageFaults:      0,
		State:           0x0004c042,
		ExecTime:        1591327084202 * time.Nanosecond,
		Name:            "hday_ecat_rt",
		Class:           "rt",
		Priority:        85,
	}
	if got := find(t, s.Threads, 3743); got != want {
		t.Errorf("hday_ecat_rt:\n got %+v\nwant %+v", got, want)
	}

	// controller_rt shows as "X" in sched/threads: relaxed, i.e. in secondary mode.
	if th := find(t, s.Threads, 3510); !th.State.Has(xenomai.StateRelax) || !th.State.Has(xenomai.StateUser) {
		t.Errorf("controller_rt state: %08x", uint32(th.State))
	}
	if th := find(t, s.Threads, 590); th.State.Has(xenomai.StateUser) {
		t.Errorf("rtnet-stack is a kernel thread: %08x", uint32(th.State))
	}
}

// sched/stat is the same thread table in a human-readable format, read moments apart from sched/acct.
// Every thread must be in both, on the same CPU, with counters that only grew from the earlier read to the later.
func TestAcctMatchesStat(t *testing.T) {
	for dir, stat_first := range map[string]bool{
		"testdata/rt":       false, // read in path order: sched/acct, then sched/stat
		"testdata/rt-later": true,  // sched/stat was read first
	} {
		t.Run(dir, func(t *testing.T) {
			s, err := xenomai.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			stat := readStat(t, dir+"/proc/xenomai/sched/stat")
			if len(stat) != len(s.Threads) {
				t.Fatalf("acct has %d threads, stat %d", len(s.Threads), len(stat))
			}
			for _, th := range s.Threads {
				row, ok := stat[th.CPU*1_000_000+th.PID]
				if th.PID == 0 {
					row, ok = stat[th.CPU*1_000_000]
				}
				if !ok {
					t.Errorf("not in stat: %+v", th)
					continue
				}
				name := strings.Trim(row.name, "[]")
				if name != th.Name {
					t.Errorf("pid %d: name %q in acct, %q in stat", th.PID, th.Name, name)
				}
				earlier, later := statRow{th.ModeSwitches, th.ContextSwitches, th.PageFaults, ""}, row
				if stat_first {
					earlier, later = later, earlier
				}
				if later.msw < earlier.msw || later.csw < earlier.csw || later.pf < earlier.pf {
					t.Errorf("%s: counters went back: acct %+v, stat %+v", th.Name, th, row)
				}
				// Moments apart: a busy thread switches a few hundred times, not millions.
				if later.csw-earlier.csw > 10_000 {
					t.Errorf("%s: acct and stat too far apart: %d context switches", th.Name, later.csw-earlier.csw)
				}
			}
		})
	}
}

func TestReadNotAvailable(t *testing.T) {
	if _, err := xenomai.Read(t.TempDir()); !errors.Is(err, xenomai.ErrNotAvailable) {
		t.Fatalf("want ErrNotAvailable, got %v", err)
	}
	if xenomai.Available(t.TempDir()) {
		t.Fatal("available without /proc/xenomai")
	}
	if !xenomai.Available("testdata/rt") {
		t.Fatal("not available with /proc/xenomai")
	}
}

func TestReadRequiresAcct(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(xenomai.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xenomai.Dir(root)+"/version", []byte("3.3.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := xenomai.Read(root); err == nil {
		t.Fatal("want an error without sched/acct")
	}
}

func TestDir(t *testing.T) {
	for root, want := range map[string]string{
		"":        "/proc/xenomai",
		"/":       "/proc/xenomai",
		"/hostfs": "/hostfs/proc/xenomai",
		"rel":     "rel/proc/xenomai",
	} {
		if got := xenomai.Dir(root); got != want {
			t.Errorf("Dir(%q) = %q, want %q", root, got, want)
		}
	}
}

func TestParseAcct(t *testing.T) {
	threads, err := xenomai.ParseAcct([]byte(
		"3 120 1 2 3 4 000480c0 10 20 30 my worker rt 50 1000000\n" +
			"\n" +
			"0 0 0 5 0 0 00018000 1 1 0 ROOT/0 idle -1 0\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []xenomai.Thread{
		{CPU: 3, PID: 120, ModeSwitches: 1, ContextSwitches: 2, Syscalls: 3, PageFaults: 4, State: 0x480c0, ExecTime: 30, Name: "my worker", Class: "rt", Priority: 50, Period: time.Millisecond},
		{CPU: 0, ContextSwitches: 5, State: 0x18000, Name: "ROOT/0", Class: "idle", Priority: -1},
	}
	if !reflect.DeepEqual(threads, want) {
		t.Errorf("\n got %+v\nwant %+v", threads, want)
	}

	for _, bad := range []string{
		"3 120 1 2 3 4 000480c0 10 20 30 short\n",
		"x 120 1 2 3 4 000480c0 10 20 30 name rt 50 0\n",
		"3 120 1 2 3 4 nothex 10 20 30 name rt 50 0\n",
	} {
		if _, err := xenomai.ParseAcct([]byte(bad)); err == nil {
			t.Errorf("want an error for %q", bad)
		}
	}
}

func TestParseFaults(t *testing.T) {
	faults, err := xenomai.ParseFaults([]byte(
		"TRAP         CPU2        CPU6\n" +
			" 14:            0           7    (Page fault)\n" +
			" 19:            1           0    (SIMD error)\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []xenomai.Fault{
		{CPU: 2, Trap: 14, Label: "Page fault"},
		{CPU: 6, Trap: 14, Label: "Page fault", Count: 7},
		{CPU: 2, Trap: 19, Label: "SIMD error", Count: 1},
		{CPU: 6, Trap: 19, Label: "SIMD error"},
	}
	if !reflect.DeepEqual(faults, want) {
		t.Errorf("\n got %+v\nwant %+v", faults, want)
	}

	if _, err := xenomai.ParseFaults([]byte("TRAP CPU0 CPU1\n 0: 1 (Divide error)\n")); err == nil {
		t.Error("want an error for a missing column")
	}
}

func TestParseClockWithExternalID(t *testing.T) {
	c, err := xenomai.ParseClock([]byte("     id: 2\ngravity: irq=100 kernel=200 user=300\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Gravity != (xenomai.Gravity{IRQ: 100, Kernel: 200, User: 300}) || c.Ticks != 0 {
		t.Errorf("%+v", c)
	}
}

func TestParseRegistry(t *testing.T) {
	if _, err := xenomai.ParseRegistry([]byte("12\n")); err == nil {
		t.Error("want an error without a slash")
	}
}

func find(t *testing.T, threads []xenomai.Thread, pid int) xenomai.Thread {
	t.Helper()
	for _, th := range threads {
		if th.PID == pid {
			return th
		}
	}
	t.Fatalf("no thread %d", pid)
	return xenomai.Thread{}
}

type statRow struct {
	msw, csw, pf uint64
	name         string
}

// readStat reads sched/stat into rows keyed by cpu*1e6+pid.
func readStat(t *testing.T, path string) map[int]statRow {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rows := map[int]statRow{}
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		c := strings.Fields(sc.Text())
		if len(c) < 9 {
			continue
		}
		cpu, _ := strconv.Atoi(c[0])
		pid, _ := strconv.Atoi(c[1])
		msw, _ := strconv.ParseUint(c[2], 10, 64)
		csw, _ := strconv.ParseUint(c[3], 10, 64)
		pf, _ := strconv.ParseUint(c[5], 10, 64)
		rows[cpu*1_000_000+pid] = statRow{msw: msw, csw: csw, pf: pf, name: strings.Join(c[8:], " ")}
	}
	return rows
}
