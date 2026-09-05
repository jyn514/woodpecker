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

// cspell:words pgup pgdown viewports

import (
	"strings"
	"time"

	"charm.land/bubbletea/v2"
)

const (
	unboundedLogRows     = maxRetainedLogBytes
	diagnosticChromeRows = 6
	stepChromeRows       = 9
)

// The zero value follows the tail; manual scrolling is local to each log.
type logViewport struct {
	offsetFromBottom int
	manual           bool
}

func displayLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (v *logViewport) appended(lines int) {
	if v.manual {
		v.offsetFromBottom += lines
	}
}

func (v *logViewport) clamp(lines, height int) {
	v.offsetFromBottom = min(max(v.offsetFromBottom, 0), max(lines-height, 0))
}

func (v *logViewport) move(key string, lines, height int) {
	switch key {
	case "pgup":
		v.manual = true
		v.offsetFromBottom += height
	case "pgdown":
		v.manual = true
		v.offsetFromBottom -= height
	case "home":
		v.manual = true
		v.offsetFromBottom = lines
	case "end", "f":
		v.manual = false
		v.offsetFromBottom = 0
	}
	v.clamp(lines, height)
}

func (v logViewport) visible(lines []string, height int) []string {
	v.clamp(len(lines), height)
	end := len(lines) - v.offsetFromBottom
	return lines[max(end-height, 0):end]
}

func (m *tuiModel) logHeight() int {
	if m.showDiagnostics {
		return m.diagnosticLogHeight()
	}
	return m.stepLogHeight()
}

func (m *tuiModel) diagnosticLogHeight() int {
	if m.height <= 0 {
		return unboundedLogRows
	}
	return max(m.height-diagnosticChromeRows, 1)
}

func (m *tuiModel) stepLogHeight() int {
	if m.height <= 0 {
		return unboundedLogRows
	}
	stepCount := 0
	for _, stage := range m.workflow.Stages {
		stepCount += len(stage.Steps)
	}
	start, end := m.visibleStepRange(stepCount)
	return max(m.height-(end-start)-stepChromeRows, 1)
}

func (m *tuiModel) activeLog() *stepLog {
	if m.showDiagnostics {
		return &m.diagnostics
	}
	steps := m.steps()
	if len(steps) == 0 {
		return nil
	}
	id := steps[m.selected].ID
	if m.logs[id] == nil {
		m.logs[id] = m.newStepLog(id)
	}
	return m.logs[id]
}

func (m *tuiModel) applyState(event stepStateChanged) {
	if !m.running {
		return
	}
	known := false
	for _, step := range m.steps() {
		if step.ID == event.StepID {
			known = true
			break
		}
	}
	if !known {
		return
	}
	old := m.states[event.StepID]
	if old != stepPending && old != stepRunning {
		return
	}
	timing := m.timings[event.StepID]
	switch event.State {
	case stepRunning:
		if old == stepRunning {
			return
		}
		timing.started = event.At
	case stepPending:
		return
	default:
		timing.finished = event.At
	}
	m.states[event.StepID] = event.State
	m.timings[event.StepID] = timing
	if event.At.After(m.now) {
		m.now = event.At
	}
}

func (m *tuiModel) scheduleTick() tea.Cmd {
	if m.tickScheduled {
		return nil
	}
	for _, state := range m.states {
		if state == stepRunning {
			m.tickScheduled = true
			return tea.Tick(time.Second, func(now time.Time) tea.Msg { return durationTickMsg(now) })
		}
	}
	return nil
}

func (log *stepLog) move(key string, height int) {
	following := !log.viewport.manual
	lineCount := len(log.lines())
	log.viewport.move(key, lineCount, height)
	if following && log.viewport.manual {
		// Paused views omit an incomplete tail. Convert the offset into that
		// row space so entering manual mode does not skip an extra line.
		pausedLineCount := len(log.lines())
		log.viewport.offsetFromBottom += pausedLineCount - lineCount
		log.viewport.clamp(pausedLineCount, height)
	}
}

func (log *stepLog) clamp(height int) {
	// Following viewports always have zero offset; unchanged retained text
	// needs no rendering merely to keep that offset valid.
	if log.viewport.manual {
		log.viewport.clamp(len(log.lines()), height)
	}
}

func (log *stepLog) finish() {
	if log.complete {
		return
	}
	if !log.viewport.manual {
		log.complete = true
		return
	}
	before := len(log.lines())
	log.complete = true
	log.viewport.appended(len(log.lines()) - before)
}

func (log *stepLog) lines() []string {
	data := log.groups.render(log.data)
	// While paused, only completed lines advance the viewport. Once execution
	// ends, expose the final unterminated line as well.
	if log.viewport.manual && !log.complete && !strings.HasSuffix(data, "\n") {
		if end := strings.LastIndexByte(data, '\n'); end >= 0 {
			data = data[:end+1]
		} else {
			data = ""
		}
	}
	return displayLines([]byte(data))
}

func (m *tuiModel) stepCommands(id string) []string {
	for _, step := range m.steps() {
		if step.ID == id {
			return step.Commands
		}
	}
	return nil
}

func (m *tuiModel) newStepLog(id string) *stepLog {
	return &stepLog{groups: newLogGroups(m.stepCommands(id))}
}

func (m *tuiModel) clampViewports() {
	height := m.stepLogHeight()
	for _, log := range m.logs {
		log.clamp(height)
	}
	m.diagnostics.clamp(m.diagnosticLogHeight())
}
