package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/lesomnus/mkot"
	"github.com/lesomnus/otx"
	"github.com/lesomnus/otx/log"
	"github.com/lesomnus/xenomai-exporter/cmd/version"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"github.com/lesomnus/z"
	"go.opentelemetry.io/otel/attribute"
	nooplog "go.opentelemetry.io/otel/log/noop"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	_ "github.com/lesomnus/mkot/otlp"
	"github.com/lesomnus/mkot/pretty"
	_ "github.com/lesomnus/mkot/pretty"
	"github.com/lesomnus/mkot/prometheus"
	_ "github.com/lesomnus/mkot/prometheus"
)

var use_config = z.NewUse[*Config]()

func WithConfig(h xli.HandlerFunc) xli.HandlerFunc {
	return func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
		if _, ok := use_config.From(ctx); ok {
			return h(ctx, cmd, next)
		}

		path_to_lookup := []string{}
		if p, ok := flg.Find[string](cmd, "config"); ok {
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("stat config file: %w", err)
			}
			path_to_lookup = append(path_to_lookup, p)
		}

		c, err := readConfig(path_to_lookup...)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}

		ctx, otx, err := c.Otel.Build(ctx)
		if err != nil {
			return fmt.Errorf("build otel: %w", err)
		}
		if err := otx.Start(ctx); err != nil {
			return fmt.Errorf("start otel: %w", err)
		}
		defer otx.Shutdown(ctx)

		l := log.From(ctx)
		l.Info("config loaded", slog.String("path", c.path))

		ctx = use_config.Into(ctx, c)
		return h(ctx, cmd, next)
	}
}

func NewCmdConfig() *xli.Command {
	return &xli.Command{
		Name: "config",
		Handler: xli.OnRun(WithConfig(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			c := use_config.Must(ctx)
			return yaml.NewEncoder(cmd).Encode(c)
		})),
	}
}

type Config struct {
	path string

	// Reads `<root_path>/proc/xenomai` instead of `/proc/xenomai`. A relative path is from the working directory.
	RootPath string `yaml:"root_path"`
	// Reports threads apart by `thread.id`; see metrics.Options.
	ThreadID bool `yaml:"thread_id"`

	Health HealthConfig
	Otel   OtelConfig
}

func readConfig(path_to_lookup ...string) (*Config, error) {
	if len(path_to_lookup) == 0 {
		path_to_lookup = []string{
			"xenomai-exporter.yaml",
			"xenomai-exporter.yml",
		}
	}

	var (
		r  io.Reader
		rp string
	)
	for _, rp = range path_to_lookup {
		f, err := os.Open(rp)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("open config file: %w", err)
		}
		r = f
		break
	}

	var c Config
	if r == nil {
		rp = "(default)"
	} else {
		// An empty file is the default config.
		if err := yaml.NewDecoder(r).Decode(&c); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("decode config: %w", err)
		}
	}
	if err := c.Evaluate(); err != nil {
		return nil, fmt.Errorf("evaluate config: %w", err)
	}

	c.path = rp
	return &c, nil
}

func (c *Config) Evaluate() error {
	return c.Health.Evaluate()
}

type HealthConfig struct {
	Enabled  *bool
	Endpoint string
}

func (c *HealthConfig) Evaluate() error {
	if c.Endpoint == "" {
		c.Endpoint = ":8081"
	}
	return nil
}

type OtelConfig struct {
	mkot.Config `yaml:",inline"`
}

func (c *OtelConfig) Build(ctx context.Context) (context.Context, *otx.Otx, error) {
	otc := mkot.NewConfig()
	if c != nil {
		otc = &c.Config
	}

	if otc.Processors == nil {
		otc.Processors = map[mkot.Id]mkot.ProcessorConfig{}
	}
	if otc.Exporters == nil {
		otc.Exporters = map[mkot.Id]mkot.ExporterConfig{}
	}
	if otc.Providers == nil {
		otc.Providers = map[mkot.Id]*mkot.ProviderConfig{}
	}

	const ServiceResourceId mkot.Id = "resource/xenomai-exporter"
	if _, ok := otc.Processors[ServiceResourceId]; !ok {
		otc.Processors[ServiceResourceId] = &mkot.Resource{
			Attributes: []mkot.Attr{
				{Key: "service.name", Value: attribute.StringValue("xenomai-exporter")},
				{Key: "service.version", Value: attribute.StringValue(version.Get().Version)},
			},
		}
	}
	if _, ok := otc.Exporters["pretty"]; !ok {
		otc.Exporters["pretty"] = pretty.ExporterConfig{}
	}
	if _, ok := otc.Exporters["prometheus/local"]; !ok {
		otc.Exporters["prometheus/local"] = &prometheus.ExporterConfig{
			Namespace: "xenomai_exporter",
			Endpoint:  ":8888",
		}
	}
	if _, ok := otc.Providers["logger"]; !ok {
		otc.Providers["logger"] = &mkot.ProviderConfig{
			Exporters: []mkot.Id{"pretty"},
		}
	}
	if _, ok := otc.Providers["meter"]; !ok {
		otc.Providers["meter"] = &mkot.ProviderConfig{
			Exporters:  []mkot.Id{"prometheus/local"},
			Processors: []mkot.Id{ServiceResourceId},
		}
	}

	resolver := mkot.Make(ctx, otc)

	tracker_provider, err := resolver.Tracer(ctx, "")
	if err != nil {
		if !errors.Is(err, mkot.ErrNotExist) {
			return nil, nil, fmt.Errorf("resolve tracer provider: %w", err)
		}
		tracker_provider = nooptrace.NewTracerProvider()
	}

	meter_provider, err := resolver.Meter(ctx, "")
	if err != nil {
		if !errors.Is(err, mkot.ErrNotExist) {
			return nil, nil, fmt.Errorf("resolve meter provider: %w", err)
		}
		meter_provider = noopmetric.NewMeterProvider()
	}

	logger_provider, err := resolver.Logger(ctx, "")
	if err != nil {
		if !errors.Is(err, mkot.ErrNotExist) {
			return nil, nil, fmt.Errorf("resolve logger provider: %w", err)
		}
		logger_provider = nooplog.NewLoggerProvider()
	}
	v := otx.New(
		otx.WithController(resolver),
		otx.WithTracerProvider(tracker_provider),
		otx.WithMeterProvider(meter_provider),
		otx.WithLoggerProvider(logger_provider),
	)
	return otx.Into(ctx, v), v, nil
}
