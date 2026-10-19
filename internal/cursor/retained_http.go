package cursor

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type RetainedHTTPScope interface {
	pluginapi.HostHTTPClient
	ID() string
	PreparePark(context.Context) error
	Close() error
}

type RetainedHTTPHost interface {
	SupportsRetainedHTTP() bool
	OpenScope(context.Context) (RetainedHTTPScope, error)
	AttachScope(context.Context, string) (RetainedHTTPScope, error)
}
