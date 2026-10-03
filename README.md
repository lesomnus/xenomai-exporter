# xenomai-exporter

Reads the statistics the [Xenomai](https://xenomai.org) 3 Cobalt core keeps in `/proc/xenomai`, which Linux tools do not see, and exports them via [OpenTelemetry](https://opentelemetry.io/) or any exporter [mkot](https://github.com/lesomnus/mkot) supports, like [prometheus](https://github.com/lesomnus/mkot/tree/e588c3260503fd56f938fe5b1867c770e3570b18/prometheus).

The same metrics are available inside an OpenTelemetry Collector with [`receiver/xenomaireceiver`](receiver/xenomaireceiver).

## Installation

### Download binary from GitHub Releases

```sh
VERSION=YYMMDD \
curl -sSLO https://github.com/lesomnus/xenomai-exporter/releases/download/${VERSION}/xenomai-exporter-amd64 \
  && chmod +x xenomai-exporter-amd64 \
  && ./xenomai-exporter-amd64 version
```

Pre-built binaries for `linux/amd64` and `linux/arm64` are attached to each [GitHub Release](https://github.com/lesomnus/xenomai-exporter/releases).

### Docker

```sh
docker run -p 8888:8888 -it --rm ghcr.io/lesomnus/xenomai-exporter:YYMMDD
```

`/proc/xenomai` is not namespaced, so it is in the container's `/proc` as well.
To read another root, mount it and set `root_path`, e.g. `-v /:/hostfs:ro` with `root_path: /hostfs`.

### Build from source

```sh
go install github.com/lesomnus/xenomai-exporter@latest
```

Requires Go 1.26+.

## Configuration

```yaml
root_path: ""     # read <root_path>/proc/xenomai; empty for /proc/xenomai
thread_id: false  # report threads apart by thread.id; see Attributes

health:
  endpoint: ":8081" # /healthz, and /readyz which fails without /proc/xenomai

otel:
  exporters:
    pretty: {}
    prometheus/local:
      endpoint: ":8888"
      namespace: xenomai_exporter

  providers:
    meter:
      exporters:
        - prometheus/local
    logger:
      exporters:
        - pretty
```

`xenomai-exporter` looks for a config file named `xenomai-exporter.yaml` (or `.yml`) in the current working directory, or the one given with `--config`.
If no config file is found, it uses the default config shown above.

`/proc/xenomai` is read whenever a reader collects: on each Prometheus scrape, or each OTLP export.
There is no interval of its own.

```yaml
otel:
  exporters:
    otlp/local:
      endpoint: localhost:4317
      tls:
        insecure: true

  providers:
    meter:
      processors:
        - resource/xenomai-exporter
      exporters:
        - otlp/local
```

`otlp` exporter can be used to push metrics to a remote OpenTelemetry Collector.

## Metrics

| Metric                            | Type    | Unit       | Attributes            | Description                                                                          |
| --------------------------------- | ------- | ---------- | --------------------- | ------------------------------------------------------------------------------------ |
| `xenomai.info`                    | gauge   |            | see below             | Always 1; the core version and clock devices are its attributes.                      |
| `xenomai.clock.gravity`           | gauge   | `s`        | `xenomai.clock.context` | How early the core clock fires timer shots, per `irq`, `kernel`, and `user` context. |
| `xenomai.faults`                  | counter | `{fault}`  | `cpu`, `xenomai.fault.trap`, `xenomai.fault.type` | Traps handled by Cobalt, e.g. `Page fault`. Only non-zero counts are reported. |
| `xenomai.heap.size`               | gauge   | `By`       | `xenomai.heap.name`, `xenomai.heap.owner` | Size of a Cobalt heap.                                  |
| `xenomai.heap.usage`              | gauge   | `By`       | `xenomai.heap.name`, `xenomai.heap.owner` | Bytes in use in a Cobalt heap.                          |
| `xenomai.registry.usage`          | gauge   | `{object}` |                       | Objects in the Cobalt registry.                                                       |
| `xenomai.registry.limit`          | gauge   | `{object}` |                       | Slots of the Cobalt registry.                                                         |
| `xenomai.cpu.utilization`         | gauge   | `1`        | `cpu`                 | Share of time a real-time CPU ran Cobalt threads, between the last two reads.        |
| `xenomai.thread.count`            | gauge   | `{thread}` | thread                | Cobalt threads.                                                                       |
| `xenomai.thread.relaxed`          | gauge   | `{thread}` | thread                | Threads in secondary mode, i.e. running under Linux, when read.                       |
| `xenomai.thread.mode_switches`    | counter | `{switch}` | thread                | Switches between primary (real-time) and secondary (Linux) mode.                      |
| `xenomai.thread.context_switches` | counter | `{switch}` | thread                | Context switches by the Cobalt scheduler.                                             |
| `xenomai.thread.syscalls`         | counter | `{call}`   | thread                | Cobalt system calls.                                                                  |
| `xenomai.thread.page_faults`      | counter | `{fault}`  | thread                | Page faults taken in primary mode.                                                    |
| `xenomai.thread.cpu.time`         | counter | `s`        | thread                | Time spent running in primary mode.                                                   |
| `xenomai.thread.period`           | gauge   | `s`        | thread                | Period of a periodic thread. Only periodic threads are reported.                      |

In Prometheus, names get the namespace and unit suffixes, e.g. `xenomai_exporter_xenomai_thread_mode_switches_total` and `xenomai_exporter_xenomai_cpu_utilization_ratio`.

Mode switches are the first thing to watch: a real-time thread that calls into Linux drops to secondary mode, where it is no longer deterministic.
Page faults in primary mode should stay at zero.

### Attributes

- **thread**: `thread.name`, `cpu`, `xenomai.thread.class` (e.g. `rt`), and `xenomai.thread.priority`.
  Threads with all four in common, such as the workers of one program, are reported together: counters and counts are summed, and the period is the longest.
  With `thread_id: true`, `thread.id` (the host TID) is added and every thread is reported apart.
  A TID changes whenever a program restarts, so every restart starts new series.
- **`xenomai.info`**: `xenomai.version`, `xenomai.affinity` (CPUs real-time threads may run on, as a hex mask), `xenomai.clock.timer`, `xenomai.clock.source`, and `xenomai.clock.watchdog`.
- **heaps**: a private heap such as `private heap[3510]` is reported as `xenomai.heap.name="private heap"` with `xenomai.heap.owner` set to the name of thread 3510, rather than its PID.

The root threads Cobalt keeps per CPU, `ROOT/<n>`, stand for Linux and are not reported as threads.
Their accounting only advances on context switches, so `xenomai.cpu.utilization` is the sum of the other threads' time on a CPU instead.

## How it reads `/proc/xenomai`

Files are only read. `clock/*` and `latency` also accept writes, which retune the core; nothing here writes to them.

| File             | Used for                                    |
| ---------------- | ------------------------------------------- |
| `version`        | `xenomai.info`                              |
| `affinity`       | `xenomai.info`                              |
| `clock/coreclk`  | `xenomai.info`, `xenomai.clock.gravity`, and the clock `xenomai.cpu.utilization` is measured on |
| `faults`         | `xenomai.faults`                            |
| `heap`           | `xenomai.heap.*`                            |
| `registry/usage` | `xenomai.registry.*`                        |
| `sched/acct`     | `xenomai.thread.*`, `xenomai.cpu.utilization` |

`sched/acct` is the parser-friendly twin of `sched/stat`.
Reading either restarts the window of the `%CPU` column of `sched/stat`, for whoever reads it next.
Only accumulated totals are used here, so other readers do not disturb these metrics,
but a person watching `sched/stat` sees `%CPU` over the time since the exporter last read.

Cobalt holds its core lock while it takes the snapshot behind each `sched/*` read.
That is brief, but it is on the real-time side: keep the reading interval in seconds, not milliseconds.

Formats follow Xenomai 3.3. Tests use `/proc/xenomai` copied from a production robot running Xenomai 3.3.3; see [`xenomai/testdata`](xenomai/testdata).
