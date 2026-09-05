// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

func TestTUIFinalSummaryRetainsCompletedWorkflows(t *testing.T) {
	m := newTUIModel(func() {})
	workflow := execWorkflow{ID: "w", Name: "build", Stages: []execStage{{Steps: []execStep{{ID: "a", Name: "test"}}}}}
	_, _ = m.Update(workflowStartedMsg(workflow))
	_, _ = m.Update(eventMsg{event: stepStateChanged{WorkflowID: "w", StepID: "a", State: stepSuccessful}})
	_, _ = m.Update(workflowFinishedMsg{id: "w"})

	assert.Equal(t, "woodpecker exec — build: successful\n  successful test\n", m.finalSummary())
}

func TestTUIWaitsForCapabilityRepliesBeforeQuitting(t *testing.T) {
	m := newTUIModel(func() {})
	_, timeout := m.Update(commandFinishedMsg{})
	require.NotNil(t, timeout)
	assert.True(t, m.finishing)

	_, quit := m.Update(tea.ModeReportMsg{Mode: ansi.ModeSynchronizedOutput})
	assert.Nil(t, quit)
	_, quit = m.Update(tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore})
	require.NotNil(t, quit)
	_, ok := quit().(tea.QuitMsg)
	assert.True(t, ok)
}

func TestRetainedLogsAreBoundedAndMarked(t *testing.T) {
	m := newTUIModel(func() {})
	m.appendLog("step", append(bytes.Repeat([]byte{'x'}, maxRetainedLogBytes), '\n'), false)
	m.appendLog("step", []byte("visible\n"), false)
	require.LessOrEqual(t, len(m.logs["step"].data), maxRetainedLogBytes)
	assert.True(t, m.logs["step"].truncated)
	assert.Contains(t, string(m.logs["step"].data), "visible")
}

func TestTUIViewKeepsSelectionAndControlsVisible(t *testing.T) {
	steps := make([]execStep, 20)
	for i := range steps {
		steps[i] = execStep{ID: fmt.Sprintf("step-%d", i), Name: strings.Repeat("long-name", 5)}
	}
	m := newTUIModel(func() {})
	_, _ = m.Update(workflowStartedMsg(execWorkflow{ID: "w", Name: "build", Stages: []execStage{{Steps: steps}}}))
	m.selected = 10
	_, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})

	view := m.View().Content
	assert.Contains(t, view, "> pending")
	assert.Contains(t, view, "q quit")
	for _, line := range strings.Split(view, "\n") {
		assert.LessOrEqual(t, len([]rune(line)), 30)
	}
	assert.LessOrEqual(t, len(strings.Split(strings.TrimSuffix(view, "\n"), "\n")), 12)
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
