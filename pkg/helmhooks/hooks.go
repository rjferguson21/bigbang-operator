// Package helmhooks defines the shared Helm/controller lifecycle contract.
package helmhooks

const (
	Enabled               = "true"
	Release               = "hooks.bigbang.dev/release"
	Integration           = "hooks.bigbang.dev/integration"
	Timeout               = "hooks.bigbang.dev/timeout-seconds"
	Phase                 = "hooks.bigbang.dev/phase"
	Action                = "hooks.bigbang.dev/action"
	Token                 = "hooks.bigbang.dev/token"
	DefaultTimeoutSeconds = 1800
)
