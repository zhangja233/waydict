package doctor

import (
	"context"

	"waydict/internal/config"
)

type Level string

const (
	OK   Level = "OK"
	Fail Level = "FAIL"
	Warn Level = "WARN"
	Info Level = "INFO"
)

type Result struct {
	Level  Level
	Name   string
	Detail string
	Err    error
}

type Registry interface {
	Checks(context.Context, config.Config) []Result
}

func errorResult(name string, err error) Result {
	if err != nil {
		return Result{Level: Fail, Name: name, Err: err}
	}
	return Result{Level: OK, Name: name}
}
