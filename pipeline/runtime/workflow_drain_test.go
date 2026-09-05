// Copyright 2026 Woodpecker Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build test

package runtime

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	pipeline_errors "go.woodpecker-ci.org/woodpecker/v3/pipeline/errors"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/state"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/tracing"
)

// A detached process can finish only after workflow destruction, and its final
// trace can still be uploading after destruction returns. Neither cancellation
// nor successful execution may return before that callback drains.
func TestRunDestroysWorkflowBeforeDrainingDetachedTrace(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "success"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				backend := &drainOrderBackend{destroyed: make(chan struct{}), foregroundStarted: make(chan struct{}), foregroundRelease: make(chan struct{})}
				releaseTrace := make(chan struct{})
				var releaseOnce sync.Once
				release := func() { releaseOnce.Do(func() { close(releaseTrace) }) }
				defer release()
				traceStarted := make(chan struct{})
				traceFinished := make(chan struct{})
				config := &backend_types.Config{Stages: []*backend_types.Stage{
					{Steps: []*backend_types.Step{{Name: "service", UUID: "service", Detached: true, OnSuccess: true, Environment: map[string]string{}}}},
					{Steps: []*backend_types.Step{{Name: "foreground", UUID: "foreground", OnSuccess: true, Environment: map[string]string{}}}},
				}}
				r := New(config, backend, WithContext(ctx), WithLogger(logging.Logger(func(_ *backend_types.Step, reader io.ReadCloser) error {
					_, err := io.Copy(io.Discard, reader)
					return err
				})), WithTracer(tracing.TraceFunc(func(s *state.State) error {
					if s.CurrStep.UUID == "service" && s.CurrStepState.Exited {
						close(traceStarted)
						<-releaseTrace
						close(traceFinished)
					}
					return nil
				})))
				done := make(chan error, 1)
				go func() { done <- r.Run(t.Context()) }()
				awaitDrainSignal(t, backend.foregroundStarted, "foreground start")
				if canceled {
					cancel(nil)
					// Let Run observe cancellation while the foreground stage is still blocked.
					synctest.Wait()
				}
				close(backend.foregroundRelease)
				// This also detects a wait-before-destroy deadlock: the detached WaitStep
				// deliberately ignores cancellation and requires actual backend teardown.
				awaitDrainSignal(t, backend.destroyed, "workflow destruction before drainage")
				awaitDrainSignal(t, traceStarted, "detached terminal trace")
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("Run returned before detached trace drained: %v", err)
				default:
				}
				release()
				select {
				case err := <-done:
					if canceled {
						require.ErrorIs(t, err, pipeline_errors.ErrCancel)
					} else {
						require.NoError(t, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("Run did not return after detached trace drained")
				}
				awaitDrainSignal(t, traceFinished, "completed detached trace")
			})
		})
	}
}

func awaitDrainSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

// Only the runtime-facing lifecycle methods are needed. Waiting for the service
// models a backend whose process exits on destruction, not merely ctx.Done().
type drainOrderBackend struct {
	backend_types.Backend
	destroyed         chan struct{}
	foregroundStarted chan struct{}
	foregroundRelease chan struct{}
}

func (*drainOrderBackend) SetupWorkflow(context.Context, *backend_types.Config, string) error {
	return nil
}

func (b *drainOrderBackend) DestroyWorkflow(context.Context, *backend_types.Config, string) error {
	close(b.destroyed)
	return nil
}

func (*drainOrderBackend) StartStep(context.Context, *backend_types.Step, string) error { return nil }

func (*drainOrderBackend) TailStep(context.Context, *backend_types.Step, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (b *drainOrderBackend) WaitStep(_ context.Context, step *backend_types.Step, _ string) (*backend_types.State, error) {
	if step.Detached {
		<-b.destroyed
	} else {
		close(b.foregroundStarted)
		<-b.foregroundRelease
	}
	return &backend_types.State{Exited: true}, nil
}

func (*drainOrderBackend) DestroyStep(context.Context, *backend_types.Step, string) error { return nil }
