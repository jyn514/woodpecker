// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"errors"
	"io"

	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	pipeline_errors "go.woodpecker-ci.org/woodpecker/v3/pipeline/errors"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/state"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/tracing"
)

func stateFromRuntime(s backend_types.State) execStepState {
	if s.Skipped {
		return stepSkipped
	}
	if !s.Exited {
		return stepRunning
	}
	if errors.Is(s.Error, context.Canceled) || errors.Is(s.Error, context.DeadlineExceeded) || errors.Is(s.Error, pipeline_errors.ErrCancel) {
		return stepCanceled
	}
	if s.OOMKilled || s.ExitCode != 0 || s.Error != nil {
		return stepFailed
	}
	return stepSuccessful
}

func eventTracer(workflowID string, submit func(execEvent)) tracing.Tracer {
	return tracing.TraceFunc(func(s *state.State) error {
		if s != nil && s.CurrStep != nil {
			submit(stepStateChanged{WorkflowID: workflowID, StepID: s.CurrStep.UUID, State: stateFromRuntime(s.CurrStepState)})
		}
		return nil
	})
}

func eventLogger(workflowID string, submit func(execEvent)) logging.Logger {
	return func(step *backend_types.Step, rc io.ReadCloser) error {
		defer rc.Close()
		buf := make([]byte, 32*1024)
		for {
			n, err := rc.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				submit(stepLogFragment{WorkflowID: workflowID, StepID: step.UUID, Data: data})
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}
}
