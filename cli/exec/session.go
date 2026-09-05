// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"io"

	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
)

type outputStream uint8

const (
	outputStdout outputStream = iota
	outputStderr
)

type execSession interface {
	Print(outputStream, string)
	Logger(string) logging.Logger
	Run(context.Context, func(context.Context) error) error
	Close() error
}

type sessionWriters struct{ stdout, stderr io.Writer }
