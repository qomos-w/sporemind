package debugcmd

import (
	"context"

	"github.com/qomos-w/sporemind/pkg/version"
)

func init() {
	Register(&versionCommand{})
}

type versionCommand struct{}

func (c *versionCommand) Name() string      { return "version" }
func (c *versionCommand) ShortHelp() string { return "Show sporemind version" }

func (c *versionCommand) Exec(_ context.Context, _ []string) (string, error) {
	return version.Version, nil
}
