// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"io"

	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/tracing"
)

type outputStream uint8

const (
	outputStdout outputStream = iota
	outputStderr
)

type execSession interface {
	Print(outputStream, string)
	BeginWorkflow(execWorkflow)
	FinishWorkflow(string, error)
	Submit(execEvent)
	Logger(string) logging.Logger
	Tracer(string) tracing.Tracer
	ExecuteWorkflow(context.Context, execWorkflow, func(context.Context) error) error
	Run(context.Context, func(context.Context) error) error
	Close() error
}

type sessionWriters struct{ stdout, stderr io.Writer }
