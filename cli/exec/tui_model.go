// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
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

type tuiModel struct {
	workflow            execWorkflow
	states              map[string]execStepState
	selected            int
	width, height       int
	running, confirming bool
	finishedErr         error
	cancel              func()
}

func newTUIModel(cancel func()) *tuiModel {
	return &tuiModel{states: map[string]execStepState{}, cancel: cancel}
}
func (*tuiModel) Init() tea.Cmd { return nil }
func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case workflowStartedMsg:
		m.workflow = execWorkflow(msg)
		m.states = map[string]execStepState{}
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
func (m *tuiModel) steps() []execStep {
	var result []execStep
	for _, stage := range m.workflow.Stages {
		result = append(result, stage.Steps...)
	}
	return result
}
func (m *tuiModel) View() tea.View {
	var b strings.Builder
	fmt.Fprintf(&b, "woodpecker exec — %s\n\n", m.workflow.Name)
	for i, step := range m.steps() {
		cursor := "  "
		if i == m.selected {
			cursor = "> "
		}
		state, ok := m.states[step.ID]
		if !ok {
			state = stepPending
		}
		fmt.Fprintf(&b, "%s%-10s %s\n", cursor, state, step.Name)
	}
	if m.confirming {
		b.WriteString("\nCancel the running workflow? (y/N)")
	} else {
		b.WriteString("\n↑/↓ select • q quit")
	}
	view := tea.NewView(b.String())
	view.AltScreen = true
	return view
}
