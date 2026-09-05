// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"fmt"

	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/tracing"
)

type plainSession struct{ sessionWriters }

func newPlainSession(w sessionWriters) *plainSession { return &plainSession{sessionWriters: w} }
func (s *plainSession) Print(stream outputStream, message string) {
	if stream == outputStderr {
		_, _ = fmt.Fprint(s.stderr, message)
	} else {
		_, _ = fmt.Fprint(s.stdout, message)
	}
}
func (*plainSession) BeginWorkflow(execWorkflow)     {}
func (*plainSession) FinishWorkflow(string, error)   {}
func (*plainSession) Submit(execEvent)               {}
func (s *plainSession) Logger(string) logging.Logger { return lineLogger(s.stderr) }
func (*plainSession) Tracer(string) tracing.Tracer   { return tracing.NoOpTracer }
func (*plainSession) ExecuteWorkflow(ctx context.Context, _ execWorkflow, execute func(context.Context) error) error {
	return execute(ctx)
}
func (*plainSession) Run(ctx context.Context, execute func(context.Context) error) error {
	return execute(ctx)
}
func (*plainSession) Close() error { return nil }
