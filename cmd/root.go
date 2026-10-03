package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/lesomnus/otx"
	"github.com/lesomnus/otx/log"
	"github.com/lesomnus/xenomai-exporter/metrics"
	"github.com/lesomnus/xenomai-exporter/xenomai"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"go.opentelemetry.io/otel/metric"
)

func NewCmdRoot() *xli.Command {
	return &xli.Command{
		Name: "xenomai-exporter",

		Flags: flg.Flags{
			&flg.String{Name: "config", Alias: 'c'},
		},
		Commands: xli.Commands{
			NewCmdVersion(),
			NewCmdConfig(),
		},
		Handler: xli.Chain(
			xli.OnRun(WithConfig(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
				c := use_config.Must(ctx)
				l := log.From(ctx)

				l.Info("reading", slog.String("dir", xenomai.Dir(c.RootPath)))
				if !xenomai.Available(c.RootPath) {
					l.Warn("xenomai is not available; nothing is reported until it is", slog.String("dir", xenomai.Dir(c.RootPath)))
				}

				if err := register(ctx, c); err != nil {
					return fmt.Errorf("register instruments: %w", err)
				}

				if c.Health.Enabled != nil && !*c.Health.Enabled {
					l.Warn("health check disabled")
					<-ctx.Done()
					return next(ctx)
				}

				l.Info("health check enabled", slog.String("endpoint", c.Health.Endpoint))
				mux := http.NewServeMux()
				mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprintln(w, "ok")
				})
				mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
					if !xenomai.Available(c.RootPath) {
						http.Error(w, "xenomai is not available", http.StatusServiceUnavailable)
						return
					}
					w.WriteHeader(http.StatusOK)
					fmt.Fprintln(w, "ok")
				})
				srv := &http.Server{
					Addr:    c.Health.Endpoint,
					Handler: mux,
				}
				go func() {
					<-ctx.Done()
					srv.Shutdown(context.Background())
				}()
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return next(ctx)
			})),
		),
	}
}

// register reads /proc/xenomai whenever a reader collects, rather than on a timer of its own,
// so the reader's interval is the only one.
func register(ctx context.Context, c *Config) error {
	meter := otx.Meter(ctx)
	collector := metrics.New(metrics.Options{ThreadID: c.ThreadID})

	instruments := map[*metrics.Desc]metric.Observable{}
	all := []metric.Observable{}
	for _, d := range metrics.All {
		inst, err := newInstrument(meter, d)
		if err != nil {
			return fmt.Errorf("%s: %w", d.Name, err)
		}
		instruments[d] = inst
		all = append(all, inst)
	}

	var warned atomic.Bool
	_, err := meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		s, err := xenomai.Read(c.RootPath)
		if err != nil {
			if errors.Is(err, xenomai.ErrNotAvailable) {
				if !warned.Swap(true) {
					log.From(ctx).Warn("xenomai is not available")
				}
				return nil
			}
			return err
		}
		warned.Store(false)

		for _, p := range collector.Collect(s) {
			opt := metric.WithAttributeSet(p.Attrs)
			switch inst := instruments[p.Desc].(type) {
			case metric.Int64Observable:
				o.ObserveInt64(inst, int64(p.Value), opt)
			case metric.Float64Observable:
				o.ObserveFloat64(inst, p.Value, opt)
			}
		}
		return nil
	}, all...)
	return err
}

func newInstrument(meter metric.Meter, d *metrics.Desc) (metric.Observable, error) {
	switch {
	case d.Kind == metrics.Counter && d.Float:
		return meter.Float64ObservableCounter(d.Name, metric.WithDescription(d.Description), metric.WithUnit(d.Unit))
	case d.Kind == metrics.Counter:
		return meter.Int64ObservableCounter(d.Name, metric.WithDescription(d.Description), metric.WithUnit(d.Unit))
	case d.Float:
		return meter.Float64ObservableGauge(d.Name, metric.WithDescription(d.Description), metric.WithUnit(d.Unit))
	default:
		return meter.Int64ObservableGauge(d.Name, metric.WithDescription(d.Description), metric.WithUnit(d.Unit))
	}
}
