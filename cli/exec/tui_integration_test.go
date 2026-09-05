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

package exec

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/dummy"
	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	pipeline_runtime "go.woodpecker-ci.org/woodpecker/v3/pipeline/runtime"
)

// Same-named parallel steps must not share duration boundaries, log bytes, or
// command matchers. The real runtime drives both adapters; only the backend's
// shell output and a concurrency barrier are supplied by the fixture.
func TestConcurrentRuntimeKeepsTUIDurationsAndGroupsByUUID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config := &backend_types.Config{Stages: []*backend_types.Stage{{Steps: []*backend_types.Step{
		{UUID: "alpha", Name: "same", Type: backend_types.StepTypeCommands, OnSuccess: true, Commands: []string{"echo alpha"}, Environment: map[string]string{}},
		{UUID: "beta", Name: "same", Type: backend_types.StepTypeCommands, OnSuccess: true, Commands: []string{"echo beta"}, Environment: map[string]string{}},
	}}}}
	backend := &concurrentEchoBackend{Backend: dummy.New(), bothStarted: make(chan struct{}), tailed: map[string]chan struct{}{"alpha": make(chan struct{}), "beta": make(chan struct{})}}
	workflow := workflowDescriptor("parallel", config)
	session := newTUISession(nil, io.Discard)
	model := newTUIModel(func() {}, session.drainLog)
	model.Update(workflowStartedMsg(workflow))
	// Callbacks enqueue only; the test goroutine is the sole model owner.
	events := make(chan execEvent, 32)
	submit := func(event execEvent) { events <- event }
	origin := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var ticks atomic.Int64
	now := func() time.Time { return origin.Add(time.Duration(ticks.Add(1)) * time.Second) }
	runtime := pipeline_runtime.New(config, backend, pipeline_runtime.WithContext(ctx),
		pipeline_runtime.WithLogger(eventLogger(workflow.ID, submit)),
		pipeline_runtime.WithTracer(eventTracer(workflow.ID, submit, now)))
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx); close(events) }()
	expected := map[string]stepTiming{}
	startsBeforeFinish := 0
	sawFinish := false
	for event := range events {
		if change, ok := event.(stepStateChanged); ok {
			timing := expected[change.StepID]
			if change.State == stepRunning {
				assert.False(t, sawFinish, "barrier must force both steps to overlap")
				startsBeforeFinish++
				timing.started = change.At
			} else {
				sawFinish = true
				require.Equal(t, stepSuccessful, change.State)
				timing.finished = change.At
			}
			expected[change.StepID] = timing
		}
		if fragment, ok := event.(stepLogFragment); ok {
			session.Submit(fragment)
			model.Update(logReadyMsg{workflowID: fragment.WorkflowID, stepID: fragment.StepID})
		} else {
			model.Update(eventMsg{event: event})
		}
	}
	require.NoError(t, <-done)
	require.NoError(t, runtime.Err())
	require.Equal(t, 2, startsBeforeFinish)
	require.Len(t, expected, 2)
	model.Update(workflowFinishedMsg{id: workflow.ID, At: now()})
	for _, step := range config.Stages[0].Steps {
		t.Run(step.UUID, func(t *testing.T) {
			timing := expected[step.UUID]
			require.False(t, timing.started.IsZero())
			require.True(t, timing.finished.After(timing.started))
			assert.Equal(t, timing, model.timings[step.UUID])
			assert.Equal(t, timing, model.completed[0].timings[step.UUID])
			assert.Equal(t, stepSuccessful, model.completed[0].states[step.UUID])
			log := model.logs[step.UUID]
			require.NotNil(t, log)
			other := "alpha"
			if step.UUID == other {
				other = "beta"
			}
			assert.Contains(t, string(log.data), "StepUUID: "+step.UUID)
			assert.NotContains(t, string(log.data), "StepUUID: "+other)
			log.groups.toggleCollapsed()
			rendered := log.groups.render(log.data)
			// The other step's echo is unmatched initialization, not a group header.
			assert.Contains(t, rendered, "+ echo "+other+"\nunmatched remains visible\n")
			assert.Contains(t, rendered, "+ echo "+step.UUID+"\n  [output collapsed]\n")
			assert.NotContains(t, rendered, "private output "+step.UUID)
		})
	}
}

// Dummy deliberately prints command metadata rather than shell echoes. Preserve
// its validation and output, appending shell-like output via the backend seam.
type concurrentEchoBackend struct {
	backend_types.Backend
	started     atomic.Int32
	bothStarted chan struct{}
	tailed      map[string]chan struct{}
}

func (b *concurrentEchoBackend) StartStep(ctx context.Context, step *backend_types.Step, task string) error {
	if err := b.Backend.StartStep(ctx, step, task); err != nil {
		return err
	}
	if b.started.Add(1) == 2 {
		close(b.bothStarted)
	}
	select {
	case <-b.bothStarted:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *concurrentEchoBackend) WaitStep(ctx context.Context, step *backend_types.Step, task string) (*backend_types.State, error) {
	select {
	case <-b.tailed[step.UUID]:
		return b.Backend.WaitStep(ctx, step, task)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *concurrentEchoBackend) TailStep(ctx context.Context, step *backend_types.Step, task string) (io.ReadCloser, error) {
	reader, err := b.Backend.TailStep(ctx, step, task)
	close(b.tailed[step.UUID])
	if err != nil {
		return nil, err
	}
	other := "alpha"
	if step.UUID == other {
		other = "beta"
	}
	output := "+ echo " + other + "\nunmatched remains visible\n+ " + step.Commands[0] + "\nprivate output " + step.UUID + "\n"
	return &echoReadCloser{Reader: io.MultiReader(reader, strings.NewReader(output)), Closer: reader}, nil
}

type echoReadCloser struct {
	io.Reader
	io.Closer
}
