package cmd

import (
	"context"

	"github.com/lesomnus/xenomai-exporter/cmd/version"
	"github.com/lesomnus/xli"
)

func NewCmdVersion() *xli.Command {
	const Template = `XENOMAI_EXPORTER_VERSION=%s
XENOMAI_EXPORTER_GIT_REV=%s
XENOMAI_EXPORTER_GIT_DIRTY=%v
`
	return &xli.Command{
		Name: "version",
		Handler: xli.OnRun(func(ctx context.Context, cmd *xli.Command, next xli.Next) error {
			v := version.Get()
			cmd.Printf(Template,
				v.Version,
				v.GitRev,
				v.GitDirty,
			)
			return nil
		}),
	}
}
