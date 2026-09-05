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

package exec

import (
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func extensionModel() *tuiModel {
	m := newTUIModel(func() {})
	m.Update(workflowStartedMsg(execWorkflow{ID: "w", Stages: []execStage{{Steps: []execStep{{ID: "a"}, {ID: "b"}, {ID: "c"}}}}}))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 15})
	return m
}
func key(m *tuiModel, text string) { m.Update(tea.KeyPressMsg(tea.Key{Text: text})) }

func TestDurationsFreezeFirstBoundariesAndSnapshotFallbacks(t *testing.T) {
	m := extensionModel()
	start := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	send := func(id string, state execStepState, at time.Time) tea.Cmd {
		_, cmd := m.Update(eventMsg{event: stepStateChanged{WorkflowID: "w", StepID: id, State: state, At: at}})
		return cmd
	}
	require.NotNil(t, send("a", stepRunning, start))
	assert.Nil(t, send("b", stepRunning, start.Add(time.Second)))
	send("a", stepRunning, start.Add(2*time.Second))
	send("a", stepSuccessful, start.Add(3*time.Second))
	send("a", stepFailed, start.Add(9*time.Second))
	assert.Equal(t, start, m.timings["a"].started)
	assert.Equal(t, start.Add(3*time.Second), m.timings["a"].finished)
	assert.Equal(t, stepSuccessful, m.states["a"])
	m.Update(workflowFinishedMsg{id: "w", At: start.Add(5 * time.Second)})
	assert.Equal(t, stepUnknown, m.states["b"])
	assert.Equal(t, stepNotRun, m.states["c"])
	_, cmd := m.Update(durationTickMsg(start.Add(6 * time.Second)))
	assert.Nil(t, cmd)
	assert.False(t, m.tickScheduled)
	assert.Contains(t, m.finalSummary(), "(3s)")
	assert.Contains(t, m.finalSummary(), "(4s)")
	m.Update(workflowStartedMsg(execWorkflow{ID: "next"}))
	assert.Contains(t, m.finalSummary(), "(4s)")
}

func TestLateTickCannotDecreaseObservedTime(t *testing.T) {
	m := extensionModel()
	at := time.Now()
	m.Update(eventMsg{event: stepStateChanged{WorkflowID: "w", StepID: "a", State: stepRunning, At: at}})
	m.Update(durationTickMsg(at.Add(-time.Second)))
	assert.Equal(t, at, m.now)
}
