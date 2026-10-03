// Package xenomai reads the statistics the Xenomai 3 Cobalt core exports under /proc/xenomai.
//
// Formats follow kernel/cobalt/{sched,clock,heap,registry,procfs}.c of Xenomai 3.3.
// Files are only read: some of them (clock/*, latency) also accept writes, which retune the core.
package xenomai

import (
	"time"
)

// Snapshot is one read of /proc/xenomai.
type Snapshot struct {
	// When the snapshot was read, by the host clock.
	Time time.Time

	// Content of `version`, e.g. "3.3.3".
	Version string
	// Content of `affinity`: the CPUs real-time threads may run on, as a hex mask, e.g. "0000ffff".
	Affinity string

	Clock    Clock
	Faults   []Fault
	Heaps    []Heap
	Registry Registry
	// Every Cobalt thread from `sched/acct`, including the per-CPU root threads.
	Threads []Thread
}

// Clock is `clock/coreclk`.
type Clock struct {
	// Anticipation applied to timer shots, per context.
	Gravity Gravity
	// Hardware timer device, e.g. "lapic-deadline".
	Timer string
	// Clock device, e.g. "<Linux clocksource>".
	Source string
	// e.g. "off".
	Watchdog string
	// Raw reading of the core clock: nanoseconds on Dovetail.
	// Zero when absent. Execution times are on the same clock.
	Ticks uint64
}

type Gravity struct {
	IRQ    time.Duration
	Kernel time.Duration
	User   time.Duration
}

// Fault is one cell of `faults`: how many times a trap occurred on a CPU.
type Fault struct {
	CPU   int
	Trap  int
	Label string // e.g. "Page fault"
	Count uint64
}

// Heap is a row of `heap`.
type Heap struct {
	// e.g. "system heap", "shared heap", "private heap[3510]".
	Name  string
	Total uint64 // bytes
	Free  uint64 // bytes
}

// Registry is `registry/usage`.
type Registry struct {
	Used  uint64
	Slots uint64
}

// Thread is a row of `sched/acct`.
type Thread struct {
	CPU int
	// Host PID (thread ID) of the shadowed Linux task; 0 for root threads.
	PID int
	// Primary/secondary mode switches.
	ModeSwitches uint64
	// Context switches.
	ContextSwitches uint64
	// Cobalt system calls.
	Syscalls uint64
	// Page faults.
	PageFaults uint64
	State      State
	// Accumulated execution time since the thread started.
	ExecTime time.Duration
	Name     string
	// Scheduling class, e.g. "rt", "idle", "weak".
	Class string
	// Current priority; -1 for root threads.
	Priority int
	// Period of a periodic thread; 0 otherwise.
	Period time.Duration
}

// State is the thread state word. Bits are from include/cobalt/uapi/kernel/thread.h.
type State uint32

const (
	StateRelax State = 0x00000080 // XNRELAX: in secondary mode, i.e. running under Linux.
	StateRoot  State = 0x00010000 // XNROOT: the root thread of a CPU, standing for Linux.
	StateUser  State = 0x00040000 // XNUSER: shadow of a user-space thread.
)

func (s State) Has(bit State) bool {
	return s&bit != 0
}
