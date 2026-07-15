//go:build !linux && !darwin

package doctor

import (
	"context"

	"waydict/internal/config"
)

type stubRegistry struct{}

func Current() Registry { return stubRegistry{} }

func (stubRegistry) Checks(context.Context, config.Config) []Result {
	return []Result{{Level: Warn, Name: "platform", Detail: "platform-specific checks are unavailable"}}
}
