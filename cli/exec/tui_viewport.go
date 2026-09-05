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
	"time"

	"charm.land/bubbletea/v2"
)

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
