// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	pipeline_errors "go.woodpecker-ci.org/woodpecker/v3/pipeline/errors"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/state"
)

func TestStateFromRuntime(t *testing.T) {
	boom := errors.New("start failed")
	tests := []struct {
		name  string
		state backend_types.State
		want  execStepState
	}{
		{"started", backend_types.State{}, stepRunning},
		{"successful", backend_types.State{Exited: true}, stepSuccessful},
		{"failed exit", backend_types.State{Exited: true, ExitCode: 1}, stepFailed},
		{"oom", backend_types.State{Exited: true, OOMKilled: true}, stepFailed},
		{"start error", backend_types.State{Exited: true, Error: boom}, stepFailed},
		{"skipped", backend_types.State{Skipped: true}, stepSkipped},
		{"canceled", backend_types.State{Exited: true, Error: context.Canceled}, stepCanceled},
		{"timeout", backend_types.State{Exited: true, Error: context.DeadlineExceeded}, stepCanceled},
		{"runtime cancellation", backend_types.State{Exited: true, Error: pipeline_errors.ErrCancel}, stepCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, stateFromRuntime(tt.state)) })
	}
}

func TestWorkflowDescriptorPreservesOrderAndUUIDIdentity(t *testing.T) {
	config := &backend_types.Config{Stages: []*backend_types.Stage{
		{Steps: []*backend_types.Step{{Name: "same", UUID: "first"}, {Name: "same", UUID: "second", Detached: true}}},
		{Steps: []*backend_types.Step{{Name: "last", UUID: "third"}}},
	}}
	got := workflowDescriptor("build", config)
	require.Len(t, got.Stages, 2)
	assert.Equal(t, []string{"first", "second"}, []string{got.Stages[0].Steps[0].ID, got.Stages[0].Steps[1].ID})
	assert.Equal(t, "third", got.Stages[1].Steps[0].ID)
	assert.True(t, got.Stages[0].Steps[1].Detached)
}

// Matching must not silently normalize shell syntax or retain mutable config slices.
func TestWorkflowDescriptorCopiesOnlyExactSingleLineCommands(t *testing.T) {
	commands := []string{"", "echo first", "echo first\necho second", "echo CR\r", " echo 'quoted'  ", " ", "echo first"}
	config := &backend_types.Config{Stages: []*backend_types.Stage{{Steps: []*backend_types.Step{{UUID: "cmd", Commands: commands}}}}}
	got := workflowDescriptor("build", config).Stages[0].Steps[0]
	commands[1] = "changed after compilation"
	assert.Equal(t, []string{"echo first", " echo 'quoted'  ", " ", "echo first"}, got.Commands)
}

// A clock read at tracer construction would give every concurrent step the same start time.
func TestEventTracerTimestampsEachIdentifiedNotification(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var events []execEvent
	tracer := eventTracer("build", func(event execEvent) { events = append(events, event) }, func() time.Time { return at })
	require.NoError(t, tracer.Trace(nil))
	require.NoError(t, tracer.Trace(&state.State{}))
	require.Empty(t, events)
	require.NoError(t, tracer.Trace(&state.State{CurrStep: &backend_types.Step{UUID: "first"}}))
	assert.Equal(t, stepStateChanged{WorkflowID: "build", StepID: "first", State: stepRunning, At: at}, events[0])
	at = at.Add(2 * time.Second)
	require.NoError(t, tracer.Trace(&state.State{CurrStep: &backend_types.Step{UUID: "detached", Detached: true}, CurrStepState: backend_types.State{Exited: true}}))
	assert.Equal(t, stepStateChanged{WorkflowID: "build", StepID: "detached", State: stepSuccessful, At: at}, events[1])
}

func TestTUISessionUsesSameClockForTraceAndCompletion(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	messages := make(chan tea.Msg, 2)
	session := newTUISession(nil, io.Discard)
	session.now = func() time.Time { return at }
	session.program = tea.NewProgram(timestampCollector{messages}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() { _, err := session.program.Run(); done <- err }()
	t.Cleanup(func() { session.program.Kill() })
	require.NoError(t, session.Tracer("build").Trace(&state.State{CurrStep: &backend_types.Step{UUID: "step"}}))
	message, ok := (<-messages).(eventMsg)
	require.True(t, ok)
	changed, ok := message.event.(stepStateChanged)
	require.True(t, ok)
	require.Equal(t, at, changed.At)
	at = at.Add(5 * time.Second)
	session.FinishWorkflow("build", nil)
	finished, ok := (<-messages).(workflowFinishedMsg)
	require.True(t, ok)
	assert.Equal(t, "build", finished.id)
	assert.Equal(t, at, finished.At)
	require.NoError(t, <-done)
}

type timestampCollector struct{ messages chan tea.Msg }

func (timestampCollector) Init() tea.Cmd { return nil }
func (m timestampCollector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case eventMsg:
		m.messages <- msg
	case workflowFinishedMsg:
		m.messages <- msg
		return m, tea.Quit
	}
	return m, nil
}
func (timestampCollector) View() tea.View { return tea.NewView("") }
