// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	pipeline_errors "go.woodpecker-ci.org/woodpecker/v3/pipeline/errors"
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
