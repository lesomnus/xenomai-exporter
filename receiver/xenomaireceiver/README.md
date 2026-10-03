# xenomai receiver

An [OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector) receiver that reads `/proc/xenomai`,
so the Xenomai Cobalt core's statistics need no separate `xenomai-exporter` process.
It produces the same metrics as `xenomai-exporter`, from the same code; see [Metrics](../../README.md#metrics).

| Status    |         |
| --------- | ------- |
| Stability | alpha   |
| Signals   | metrics |

## Build

Add it to an [ocb](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder) manifest:

```yaml
receivers:
  - gomod: github.com/lesomnus/xenomai-exporter/receiver/xenomaireceiver v0.0.0-<date>-<commit>
```

Use a pseudo-version of a commit on `main`, e.g. from `go list -m github.com/lesomnus/xenomai-exporter/receiver/xenomaireceiver@main`.

## Configuration

```yaml
receivers:
  xenomai:
    collection_interval: 15s
```

| Field                 | Default | Description                                                                 |
| --------------------- | ------- | --------------------------------------------------------------------------- |
| `collection_interval` | `1m`    | How often to read `/proc/xenomai`. Keep it in seconds; see the main README. |
| `initial_delay`       | `1s`    | Delay before the first read.                                                |
| `timeout`             | `0s`    | Timeout of each read; `0s` means no timeout.                                |
| `root_path`           |         | Read `<root_path>/proc/xenomai`, like `hostmetrics`. Must be absolute.       |
| `thread_id`           | `false` | Report threads apart by `thread.id`; see [Attributes](../../README.md#attributes). |

Where there is no `/proc/xenomai`, the receiver logs that once and reports nothing,
so the same configuration can run on nodes without Xenomai.

`xenomai.cpu.utilization` needs two reads, so it starts from the second scrape.
Counters start when the receiver does, or, for a series that appears or resets later, such as a restarted thread's, at the previous scrape.

## Running in a container

`/proc/xenomai` is not namespaced, so the Collector's own `/proc` has it.
If the host's root is mounted already, e.g. for `hostmetrics`, `root_path` can point there as well:

```yaml
receivers:
  xenomai:
    root_path: /hostfs
```
