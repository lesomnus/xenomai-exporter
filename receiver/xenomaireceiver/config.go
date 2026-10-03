package xenomaireceiver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/collector/scraper/scraperhelper"
)

type Config struct {
	scraperhelper.ControllerConfig `mapstructure:",squash"`

	// Reads `<root_path>/proc/xenomai` instead of `/proc/xenomai`, like `hostmetrics`.
	RootPath string `mapstructure:"root_path"`

	// Reports threads apart by `thread.id`, which changes on every restart.
	ThreadID bool `mapstructure:"thread_id"`
}

func (c *Config) Validate() error {
	if c.RootPath == "" {
		return nil
	}
	if !filepath.IsAbs(c.RootPath) {
		return fmt.Errorf(`"root_path" must be absolute: %q`, c.RootPath)
	}
	info, err := os.Stat(c.RootPath)
	if err != nil {
		return fmt.Errorf(`"root_path": %w`, err)
	}
	if !info.IsDir() {
		return errors.New(`"root_path" is not a directory`)
	}
	return nil
}
