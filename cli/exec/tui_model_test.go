// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTUIModelSelectionStateAndResize(t *testing.T) {
	m := newTUIModel(func() {})
	_, _ = m.Update(workflowStartedMsg(execWorkflow{ID: "w", Name: "build", Stages: []execStage{{Steps: []execStep{{ID: "a", Name: "first"}, {ID: "b", Name: "second"}}}}}))
	_, _ = m.Update(eventMsg{event: stepStateChanged{WorkflowID: "w", StepID: "a", State: stepSuccessful}})
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	assert.Equal(t, 1, m.selected)
	assert.Equal(t, 80, m.width)
	assert.Contains(t, m.View().Content, "successful first")
}

func TestRetainedLogsAreBoundedAndMarked(t *testing.T) {
	m := newTUIModel(func() {})
	m.appendLog("step", append(bytes.Repeat([]byte{'x'}, maxRetainedLogBytes), '\n'), false)
	m.appendLog("step", []byte("visible\n"), false)
	require.LessOrEqual(t, len(m.logs["step"].data), maxRetainedLogBytes)
	assert.True(t, m.logs["step"].truncated)
	assert.Contains(t, string(m.logs["step"].data), "visible")
}

func TestIngressIsBoundedAndStopsAcceptingAfterClose(t *testing.T) {
	s := newTUISession(bytes.NewReader(nil), &bytes.Buffer{})
	s.Submit(stepLogFragment{WorkflowID: "w", StepID: "s", Data: bytes.Repeat([]byte{'x'}, maxPendingLogBytes*2)})
	data, truncated := s.drainLog("w", "s")
	assert.Len(t, data, maxPendingLogBytes)
	assert.True(t, truncated)
	s.closed = true
	s.Submit(stepLogFragment{WorkflowID: "w", StepID: "s", Data: []byte("ignored")})
	data, _ = s.drainLog("w", "s")
	assert.Empty(t, data)
}
