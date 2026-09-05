// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"bytes"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type workflowStartedMsg execWorkflow
type workflowFinishedMsg struct {
	id  string
	err error
}
type commandFinishedMsg struct{ err error }
type eventMsg struct{ event execEvent }
type logReadyMsg struct{ workflowID, stepID string }

const (
	maxPendingLogBytes  = 256 * 1024
	maxRetainedLogBytes = 1024 * 1024
)

type stepLog struct {
	data      []byte
	truncated bool
}
type tuiModel struct {
	workflow            execWorkflow
	states              map[string]execStepState
	logs                map[string]*stepLog
	selected            int
	width, height       int
	running, confirming bool
	finishedErr         error
	cancel              func()
	drainLog            func(string, string) ([]byte, bool)
}

func newTUIModel(cancel func(), drains ...func(string, string) ([]byte, bool)) *tuiModel {
	m := &tuiModel{states: map[string]execStepState{}, logs: map[string]*stepLog{}, cancel: cancel}
	if len(drains) != 0 {
		m.drainLog = drains[0]
	}
	return m
}
func (*tuiModel) Init() tea.Cmd { return nil }
func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case workflowStartedMsg:
		m.workflow = execWorkflow(msg)
		m.states = map[string]execStepState{}
		m.logs = map[string]*stepLog{}
		m.selected = 0
		m.running = true
		m.confirming = false
	case workflowFinishedMsg:
		m.running = false
		m.finishedErr = msg.err
	case commandFinishedMsg:
		m.running = false
		m.finishedErr = msg.err
		return m, tea.Quit
	case eventMsg:
		if state, ok := msg.event.(stepStateChanged); ok && state.WorkflowID == m.workflow.ID {
			m.states[state.StepID] = state.State
		}
	case logReadyMsg:
		if msg.workflowID == m.workflow.ID && m.drainLog != nil {
			data, truncated := m.drainLog(msg.workflowID, msg.stepID)
			m.appendLog(msg.stepID, data, truncated)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected+1 < len(m.steps()) {
				m.selected++
			}
		case "ctrl+c":
			if m.running {
				m.cancel()
			}
		case "q":
			if m.running {
				m.confirming = true
			} else {
				return m, tea.Quit
			}
		case "y":
			if m.confirming {
				m.confirming = false
				m.cancel()
			}
		case "n", "esc":
			m.confirming = false
		}
	}
	return m, nil
}
func (m *tuiModel) appendLog(id string, data []byte, ingressTruncated bool) {
	log := m.logs[id]
	if log == nil {
		log = &stepLog{}
		m.logs[id] = log
	}
	log.data = append(log.data, data...)
	log.truncated = log.truncated || ingressTruncated
	if len(log.data) <= maxRetainedLogBytes {
		return
	}
	over := len(log.data) - maxRetainedLogBytes
	if newline := bytes.IndexByte(log.data[over:], '\n'); newline >= 0 {
		over += newline + 1
	}
	log.data = append([]byte(nil), log.data[over:]...)
	if len(log.data) > maxRetainedLogBytes {
		log.data = append([]byte(nil), log.data[len(log.data)-maxRetainedLogBytes:]...)
	}
	log.truncated = true
}
func (m *tuiModel) steps() []execStep {
	var result []execStep
	for _, stage := range m.workflow.Stages {
		result = append(result, stage.Steps...)
	}
	return result
}
func (m *tuiModel) View() tea.View {
	var b strings.Builder
	writeViewLine(&b, m.width, fmt.Sprintf("woodpecker exec — %s", m.workflow.Name))
	b.WriteByte('\n')

	steps := m.steps()
	start, end := m.visibleStepRange(len(steps))
	if start > 0 {
		writeViewLine(&b, m.width, fmt.Sprintf("  … %d earlier steps", start))
	}
	for i := start; i < end; i++ {
		step := steps[i]
		cursor := "  "
		if i == m.selected {
			cursor = "> "
		}
		state, ok := m.states[step.ID]
		if !ok {
			state = stepPending
		}
		writeViewLine(&b, m.width, fmt.Sprintf("%s%-10s %s", cursor, state, step.Name))
	}
	if end < len(steps) {
		writeViewLine(&b, m.width, fmt.Sprintf("  … %d later steps", len(steps)-end))
	}

	if len(steps) != 0 {
		b.WriteString("\nLogs\n")
		if log := m.logs[steps[m.selected].ID]; log != nil {
			if log.truncated {
				writeViewLine(&b, m.width, "[earlier output truncated]")
			}
			lines := strings.Split(string(log.data), "\n")
			if m.height > 0 {
				limit := max(m.height-(end-start)-9, 1)
				if len(lines) > limit {
					lines = lines[len(lines)-limit:]
				}
			}
			for i, line := range lines {
				if i == len(lines)-1 && line == "" {
					continue
				}
				writeViewLine(&b, m.width, line)
			}
		}
	}
	if m.confirming {
		writeViewLine(&b, m.width, "Cancel this command and its running workflow? (y/N)")
	} else {
		writeViewLine(&b, m.width, "↑/↓ select • q quit")
	}
	view := tea.NewView(b.String())
	view.AltScreen = true
	return view
}

func (m *tuiModel) visibleStepRange(stepCount int) (int, int) {
	if m.height <= 0 {
		return 0, stepCount
	}
	limit := max(m.height/3, 1)
	limit = min(limit, max(m.height-8, 1), stepCount)
	start := max(m.selected-limit/2, 0)
	end := min(start+limit, stepCount)
	start = max(end-limit, 0)
	return start, end
}

func writeViewLine(b *strings.Builder, width int, line string) {
	if width > 0 {
		runes := []rune(line)
		if len(runes) > width {
			if width == 1 {
				line = "…"
			} else {
				line = string(runes[:width-1]) + "…"
			}
		}
	}
	b.WriteString(line)
	b.WriteByte('\n')
}
