package xenomaireceiver

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/scraper"
	"go.opentelemetry.io/collector/scraper/scraperhelper"
)

const ScopeName = "github.com/lesomnus/xenomai-exporter/receiver/xenomaireceiver"

var Type = component.MustNewType("xenomai")

func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		Type,
		createDefaultConfig,
		receiver.WithMetrics(createMetrics, component.StabilityLevelAlpha),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ControllerConfig: scraperhelper.NewDefaultControllerConfig(),
	}
}

func createMetrics(_ context.Context, set receiver.Settings, cfg component.Config, next consumer.Metrics) (receiver.Metrics, error) {
	c := cfg.(*Config)
	s := newScraper(c, set.Logger)
	sc, err := scraper.NewMetrics(s.scrape, scraper.WithStart(s.start))
	if err != nil {
		return nil, err
	}
	return scraperhelper.NewMetricsController(&c.ControllerConfig, set, next,
		scraperhelper.AddMetricsScraper(Type, sc),
	)
}
