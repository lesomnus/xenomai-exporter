// Package metrics defines the metrics made from /proc/xenomai, once, for both the exporter and the Collector receiver.
package metrics

type Kind int

const (
	Gauge Kind = iota
	// Counter is monotonic and cumulative; it may reset when what it counts goes away.
	Counter
)

type Desc struct {
	Name        string
	Description string
	Unit        string
	Kind        Kind
	// Float values; integers otherwise.
	Float bool
	// Points with the same attributes are merged by taking the maximum; by summing otherwise.
	Max bool
}

var (
	Info = &Desc{
		Name:        "xenomai.info",
		Description: "Xenomai core version and clock devices, as attributes. Always 1.",
		// No unit: "1" would make it a ratio, e.g. `xenomai_info_ratio` in Prometheus.
	}
	ClockGravity = &Desc{
		Name:        "xenomai.clock.gravity",
		Description: "How early the core clock fires timer shots to absorb latency, per context.",
		Unit:        "s",
		Float:       true,
	}
	Faults = &Desc{
		Name:        "xenomai.faults",
		Description: "Traps handled by the Cobalt core, per CPU and trap. Only non-zero counts are reported.",
		Unit:        "{fault}",
		Kind:        Counter,
	}
	HeapSize = &Desc{
		Name:        "xenomai.heap.size",
		Description: "Size of a Cobalt heap.",
		Unit:        "By",
	}
	HeapUsage = &Desc{
		Name:        "xenomai.heap.usage",
		Description: "Bytes in use in a Cobalt heap.",
		Unit:        "By",
	}
	RegistryUsage = &Desc{
		Name:        "xenomai.registry.usage",
		Description: "Objects in the Cobalt registry.",
		Unit:        "{object}",
	}
	RegistryLimit = &Desc{
		Name:        "xenomai.registry.limit",
		Description: "Slots of the Cobalt registry.",
		Unit:        "{object}",
	}
	CPUUtilization = &Desc{
		Name:        "xenomai.cpu.utilization",
		Description: "Share of time a real-time CPU ran Cobalt threads, between the last two reads.",
		Unit:        "1",
		Float:       true,
	}
	ThreadCount = &Desc{
		Name:        "xenomai.thread.count",
		Description: "Cobalt threads.",
		Unit:        "{thread}",
	}
	ThreadRelaxed = &Desc{
		Name:        "xenomai.thread.relaxed",
		Description: "Cobalt threads in secondary mode, i.e. running under Linux, when read.",
		Unit:        "{thread}",
	}
	ThreadModeSwitches = &Desc{
		Name:        "xenomai.thread.mode_switches",
		Description: "Switches between primary (real-time) and secondary (Linux) mode.",
		Unit:        "{switch}",
		Kind:        Counter,
	}
	ThreadContextSwitches = &Desc{
		Name:        "xenomai.thread.context_switches",
		Description: "Context switches by the Cobalt scheduler.",
		Unit:        "{switch}",
		Kind:        Counter,
	}
	ThreadSyscalls = &Desc{
		Name:        "xenomai.thread.syscalls",
		Description: "Cobalt system calls.",
		Unit:        "{call}",
		Kind:        Counter,
	}
	ThreadPageFaults = &Desc{
		Name:        "xenomai.thread.page_faults",
		Description: "Page faults taken in primary mode.",
		Unit:        "{fault}",
		Kind:        Counter,
	}
	ThreadCPUTime = &Desc{
		Name:        "xenomai.thread.cpu.time",
		Description: "Time spent running in primary mode.",
		Unit:        "s",
		Kind:        Counter,
		Float:       true,
	}
	ThreadPeriod = &Desc{
		Name:        "xenomai.thread.period",
		Description: "Period of a periodic Cobalt thread. Only periodic threads are reported.",
		Unit:        "s",
		Float:       true,
		Max:         true,
	}
)

// All lists every metric, in the order they are reported.
var All = []*Desc{
	Info,
	ClockGravity,
	Faults,
	HeapSize,
	HeapUsage,
	RegistryUsage,
	RegistryLimit,
	CPUUtilization,
	ThreadCount,
	ThreadRelaxed,
	ThreadModeSwitches,
	ThreadContextSwitches,
	ThreadSyscalls,
	ThreadPageFaults,
	ThreadCPUTime,
	ThreadPeriod,
}

// Attribute keys.
const (
	AttrVersion       = "xenomai.version"
	AttrAffinity      = "xenomai.affinity"
	AttrClockTimer    = "xenomai.clock.timer"
	AttrClockSource   = "xenomai.clock.source"
	AttrClockWatchdog = "xenomai.clock.watchdog"
	AttrClockContext  = "xenomai.clock.context" // irq, kernel, or user
	AttrCPU           = "cpu"
	AttrFaultTrap     = "xenomai.fault.trap"
	AttrFaultType     = "xenomai.fault.type"
	AttrHeapName      = "xenomai.heap.name"
	AttrHeapOwner     = "xenomai.heap.owner" // thread name owning a private heap
	AttrThreadName    = "thread.name"
	AttrThreadID      = "thread.id" // only with Options.ThreadID
	AttrThreadClass   = "xenomai.thread.class"
	AttrThreadPrio    = "xenomai.thread.priority"
)
