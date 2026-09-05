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
	"fmt"
	"strings"
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

func TestViewportKeepsIndependentScrollAndConfirmation(t *testing.T) {
	m := extensionModel()
	m.appendLog("a", []byte("0\n1\n2\n3\n4\n5\n6\n"), false)
	log := m.activeLog()
	log.viewport.move("home", len(displayLines(log.data)), m.logHeight())
	before := log.viewport.visible(displayLines(log.data), m.logHeight())
	m.appendLog("a", []byte("7\n"), false)
	assert.Equal(t, before, log.viewport.visible(displayLines(log.data), m.logHeight()))
	offset := log.viewport.offsetFromBottom
	m.appendLog("a", []byte("partial"), false)
	assert.Equal(t, offset, log.viewport.offsetFromBottom)
	m.selected = 1
	assert.False(t, m.activeLog().viewport.manual)
	m.selected = 0
	assert.True(t, m.activeLog().viewport.manual)
	key(m, "f")
	assert.False(t, log.viewport.manual)
	key(m, "q")
	key(m, "d")
	key(m, "j")
	assert.False(t, m.showDiagnostics)
	assert.Zero(t, m.selected)
	key(m, "n")
	key(m, "d")
	assert.True(t, m.showDiagnostics)
	m.Update(workflowStartedMsg(execWorkflow{ID: "next"}))
	assert.Empty(t, m.logs)
}

func TestViewportClampsAndFollows(t *testing.T) {
	v := logViewport{}
	v.move("home", 20, 5)
	assert.Equal(t, 15, v.offsetFromBottom)
	v.clamp(8, 5)
	assert.Equal(t, 3, v.offsetFromBottom)
	v.move("f", 8, 5)
	assert.Equal(t, []string{"d", "e"}, v.visible([]string{"a", "b", "c", "d", "e"}, 2))
}

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

func TestPausedLogSurvivesEvictionAndPartialLines(t *testing.T) {
	m := extensionModel()
	var data strings.Builder
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&data, "%04d%s\n", i, strings.Repeat("x", 1000))
	}
	m.appendLog("a", []byte(data.String()), false)
	log := m.activeLog()
	log.viewport.move("pgup", len(log.lines()), m.logHeight())
	before := log.viewport.visible(log.lines(), m.logHeight())
	m.appendLog("a", []byte(strings.Repeat("y", 2000)+"\n"), false)
	assert.Equal(t, before, log.viewport.visible(log.lines(), m.logHeight()))
	m.appendLog("a", []byte("partial"), false)
	assert.Equal(t, before, log.viewport.visible(log.lines(), m.logHeight()))
	m.appendLog("a", []byte("\n"), false)
	assert.Equal(t, before, log.viewport.visible(log.lines(), m.logHeight()))
}

func TestLateTickCannotDecreaseObservedTime(t *testing.T) {
	m := extensionModel()
	at := time.Now()
	m.Update(eventMsg{event: stepStateChanged{WorkflowID: "w", StepID: "a", State: stepRunning, At: at}})
	m.Update(durationTickMsg(at.Add(-time.Second)))
	assert.Equal(t, at, m.now)
}
