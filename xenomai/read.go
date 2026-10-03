package xenomai

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNotAvailable means there is no /proc/xenomai, i.e. the kernel runs no Cobalt core.
var ErrNotAvailable = errors.New("xenomai is not available")

// Dir returns the path of /proc/xenomai under `root`; an empty root means `/`.
func Dir(root string) string {
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, "proc", "xenomai")
}

// Available reports whether /proc/xenomai exists under `root`.
func Available(root string) bool {
	_, err := os.Stat(Dir(root))
	return err == nil
}

// Read reads /proc/xenomai under `root`.
// Files other than `version` and `sched/acct` are optional: a missing one leaves its fields zero.
func Read(root string) (*Snapshot, error) {
	dir := Dir(root)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotAvailable
		}
		return nil, err
	}

	s := &Snapshot{Time: time.Now()}
	read := func(name string, required bool, parse func([]byte) error) error {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if !required && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := parse(b); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}

	errs := []error{
		read("version", true, func(b []byte) error {
			s.Version = strings.TrimSpace(string(b))
			return nil
		}),
		read("affinity", false, func(b []byte) error {
			s.Affinity = strings.TrimSpace(string(b))
			return nil
		}),
		read("clock/coreclk", false, func(b []byte) (err error) {
			s.Clock, err = ParseClock(b)
			return
		}),
		read("faults", false, func(b []byte) (err error) {
			s.Faults, err = ParseFaults(b)
			return
		}),
		read("heap", false, func(b []byte) (err error) {
			s.Heaps, err = ParseHeaps(b)
			return
		}),
		read("registry/usage", false, func(b []byte) (err error) {
			s.Registry, err = ParseRegistry(b)
			return
		}),
		read("sched/acct", true, func(b []byte) (err error) {
			s.Threads, err = ParseAcct(b)
			return
		}),
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return s, nil
}

// ParseAcct parses `sched/acct`, printed by vfile_schedacct_show as
//
//	cpu pid ssw csw xsc pf state account_period exectime_period exectime_total name class cprio period
//
// Times are nanoseconds. The period fields are since the previous read of `sched/acct` or `sched/stat` by anyone,
// so only `exectime_total` is used.
func ParseAcct(b []byte) ([]Thread, error) {
	threads := []Thread{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 14 {
			return nil, fmt.Errorf("line %d: want 14 fields, got %d", n, len(f))
		}
		// A name may contain spaces; the fields after it are fixed.
		tail := f[len(f)-3:]

		var (
			t    Thread
			errs []error
		)
		atoi := func(s string) int {
			v, err := strconv.Atoi(s)
			errs = append(errs, err)
			return v
		}
		atou := func(s string) uint64 {
			v, err := strconv.ParseUint(s, 10, 64)
			errs = append(errs, err)
			return v
		}

		t.CPU = atoi(f[0])
		t.PID = atoi(f[1])
		t.ModeSwitches = atou(f[2])
		t.ContextSwitches = atou(f[3])
		t.Syscalls = atou(f[4])
		t.PageFaults = atou(f[5])
		state, err := strconv.ParseUint(f[6], 16, 32)
		errs = append(errs, err)
		t.State = State(state)
		t.ExecTime = time.Duration(atou(f[9]))
		t.Name = strings.Join(f[10:len(f)-3], " ")
		t.Class = tail[0]
		t.Priority = atoi(tail[1])
		t.Period = time.Duration(atou(tail[2]))
		if err := errors.Join(errs...); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		threads = append(threads, t)
	}
	return threads, sc.Err()
}

// ParseClock parses `clock/coreclk`, printed by clock_show as lines of `<key>: <value>`:
//
//	gravity: irq=0 kernel=3350 user=3350
//	 devices: timer=lapic-deadline, clock=<Linux clocksource>
//	watchdog: off
//	  ticks: 4375800710602 (03fa d20c05ca)
func ParseClock(b []byte) (Clock, error) {
	c := Clock{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "gravity":
			for _, kv := range strings.Fields(value) {
				k, v, _ := strings.Cut(kv, "=")
				ns, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return c, fmt.Errorf("gravity %q: %w", kv, err)
				}
				switch k {
				case "irq":
					c.Gravity.IRQ = time.Duration(ns)
				case "kernel":
					c.Gravity.Kernel = time.Duration(ns)
				case "user":
					c.Gravity.User = time.Duration(ns)
				}
			}
		case "devices":
			for _, kv := range strings.Split(value, ",") {
				k, v, _ := strings.Cut(strings.TrimSpace(kv), "=")
				switch k {
				case "timer":
					c.Timer = v
				case "clock":
					c.Source = v
				}
			}
		case "watchdog":
			c.Watchdog = value
		case "ticks":
			v, _, _ := strings.Cut(value, " ")
			ticks, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return c, fmt.Errorf("ticks %q: %w", value, err)
			}
			c.Ticks = ticks
		}
	}
	return c, sc.Err()
}

// ParseFaults parses `faults`, printed by faults_vfile_show as a table with a column per real-time CPU:
//
//	TRAP         CPU0        CPU1
//	  0:            0           0    (Divide error)
func ParseFaults(b []byte) ([]Fault, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	if !sc.Scan() {
		return nil, sc.Err()
	}
	cpus := []int{}
	for _, h := range strings.Fields(sc.Text())[1:] {
		cpu, err := strconv.Atoi(strings.TrimPrefix(h, "CPU"))
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", h, err)
		}
		cpus = append(cpus, cpu)
	}

	faults := []Fault{}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		label := ""
		if i := strings.Index(line, "("); i >= 0 {
			label = strings.TrimSuffix(strings.TrimSpace(line[i+1:]), ")")
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) != len(cpus)+1 {
			return nil, fmt.Errorf("want %d columns, got %d: %q", len(cpus)+1, len(f), line)
		}
		trap, err := strconv.Atoi(strings.TrimSuffix(f[0], ":"))
		if err != nil {
			return nil, fmt.Errorf("trap %q: %w", f[0], err)
		}
		for i, cpu := range cpus {
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("trap %d: %w", trap, err)
			}
			faults = append(faults, Fault{CPU: cpu, Trap: trap, Label: label, Count: n})
		}
	}
	return faults, sc.Err()
}

// ParseHeaps parses `heap`, printed by the heap vfile as
//
//	  TOTAL      FREE  NAME
//	4194304   4134848  system heap
func ParseHeaps(b []byte) ([]Heap, error) {
	heaps := []Heap{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || f[0] == "TOTAL" {
			continue
		}
		total, err1 := strconv.ParseUint(f[0], 10, 64)
		free, err2 := strconv.ParseUint(f[1], 10, 64)
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		heaps = append(heaps, Heap{Name: strings.Join(f[2:], " "), Total: total, Free: free})
	}
	return heaps, sc.Err()
}

// ParseRegistry parses `registry/usage`, printed as `<active>/<slots>`.
func ParseRegistry(b []byte) (Registry, error) {
	used, slots, ok := strings.Cut(strings.TrimSpace(string(b)), "/")
	if !ok {
		return Registry{}, fmt.Errorf("want <used>/<slots>: %q", b)
	}
	u, err1 := strconv.ParseUint(used, 10, 64)
	s, err2 := strconv.ParseUint(slots, 10, 64)
	return Registry{Used: u, Slots: s}, errors.Join(err1, err2)
}
