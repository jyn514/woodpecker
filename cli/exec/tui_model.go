// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

// cspell:words pgup pgdown viewports

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type (
	workflowStartedMsg  execWorkflow
	workflowFinishedMsg struct {
		id  string
		err error
		At  time.Time
	}
)

type (
	commandFinishedMsg   struct{ err error }
	probeDrainTimeoutMsg struct{}
	durationTickMsg      time.Time
)

type stepTiming struct {
	started, finished time.Time
}

func (t stepTiming) label(now time.Time) string {
	if t.started.IsZero() {
		return ""
	}
	end := t.finished
	if end.IsZero() {
		end = now
	}
	return " (" + max(end.Sub(t.started), 0).Round(time.Second).String() + ")"
}

type (
	eventMsg    struct{ event execEvent }
	logReadyMsg struct{ workflowID, stepID string }
)

const (
	maxPendingLogBytes  = 256 * 1024
	maxRetainedLogBytes = 1024 * 1024
	maxDiagnosticBytes  = 256 * 1024
)

type stepLog struct {
	data      []byte
	truncated bool
	viewport  logViewport
	groups    logGroups
	complete  bool
}

type completedWorkflow struct {
	workflow execWorkflow
	states   map[string]execStepState
	timings  map[string]stepTiming
	err      error
}

type tuiModel struct {
	workflow            execWorkflow
	states              map[string]execStepState
	logs                map[string]*stepLog
	timings             map[string]stepTiming
	now                 time.Time
	tickScheduled       bool
	selected            int
	width, height       int
	running, confirming bool
	finishedErr         error
	finishing           bool
	syncReportSeen      bool
	unicodeReportSeen   bool
	completed           []completedWorkflow
	diagnostics         stepLog
	showDiagnostics     bool
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
		m.timings = map[string]stepTiming{}
		m.selected = 0
		m.running = true
		m.confirming = false
	case workflowFinishedMsg:
		if msg.id != m.workflow.ID || !m.running {
			return m, nil
		}
		for _, step := range m.steps() {
			state := m.states[step.ID]
			timing := m.timings[step.ID]
			switch state {
			case stepRunning:
				m.states[step.ID] = stepUnknown
				timing.finished = msg.At
				m.timings[step.ID] = timing
			case stepPending:
				m.states[step.ID] = stepNotRun
			}
		}
		m.running = false
		for _, log := range m.logs {
			log.finish()
		}
		m.clampViewports()
		m.finishedErr = msg.err
		states := make(map[string]execStepState, len(m.states))
		for id, state := range m.states {
			states[id] = state
		}
		m.completed = append(m.completed, completedWorkflow{workflow: m.workflow, states: states, timings: m.timings, err: msg.err})
	case commandFinishedMsg:
		m.diagnostics.finish()
		m.clampViewports()
		m.running = false
		m.finishedErr = msg.err
		m.finishing = true
		if m.probesDrained() {
			return m, tea.Quit
		}
		return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return probeDrainTimeoutMsg{} })
	case probeDrainTimeoutMsg:
		if m.finishing {
			return m, tea.Quit
		}
	case tea.ModeReportMsg:
		switch msg.Mode {
		case ansi.ModeSynchronizedOutput:
			m.syncReportSeen = true
		case ansi.ModeUnicodeCore:
			m.unicodeReportSeen = true
		}
		if m.finishing && m.probesDrained() {
			return m, tea.Quit
		}
	case durationTickMsg:
		m.tickScheduled = false
		if time.Time(msg).After(m.now) {
			m.now = time.Time(msg)
		}
		return m, m.scheduleTick()
	case eventMsg:
		switch event := msg.event.(type) {
		case stepStateChanged:
			if event.WorkflowID == m.workflow.ID {
				m.applyState(event)
				return m, m.scheduleTick()
			}
		case diagnosticFragment:
			m.appendDiagnostic(event.Data)
		}
	case logReadyMsg:
		if msg.workflowID == m.workflow.ID && m.drainLog != nil {
			data, truncated := m.drainLog(msg.workflowID, msg.stepID)
			m.appendLog(msg.stepID, data, truncated)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampViewports()
	case tea.KeyPressMsg:
		key := msg.String()
		if m.confirming && key != "y" && key != "n" && key != "esc" && key != "ctrl+c" {
			return m, nil
		}
		switch key {
		case "c":
			if !m.showDiagnostics {
				if log := m.activeLog(); log != nil {
					log.groups.toggleCollapsed()
					m.clampViewports()
				}
			}
			return m, nil
		case "pgup", "pgdown", "home", "end", "f":
			log := m.activeLog()
			if log != nil {
				log.viewport.move(key, len(log.lines()), m.logHeight())
			}
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if !m.showDiagnostics && m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if !m.showDiagnostics && m.selected+1 < len(m.steps()) {
				m.selected++
			}
		case "d":
			m.showDiagnostics = !m.showDiagnostics
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

func (m *tuiModel) probesDrained() bool {
	return m.syncReportSeen && m.unicodeReportSeen
}

func (m *tuiModel) finalSummary() string {
	if len(m.completed) == 0 {
		return ""
	}
	var b strings.Builder
	for i, result := range m.completed {
		if i > 0 {
			b.WriteByte('\n')
		}
		status := "successful"
		if result.err != nil {
			status = "failed"
		}
		fmt.Fprintf(&b, "woodpecker exec — %s: %s\n", result.workflow.Name, status)
		for _, stage := range result.workflow.Stages {
			for _, step := range stage.Steps {
				state, ok := result.states[step.ID]
				if !ok {
					state = stepPending
				}
				fmt.Fprintf(&b, "  %-10s %s%s\n", state, step.Name, result.timings[step.ID].label(m.now))
			}
		}
	}
	return b.String()
}

func (m *tuiModel) appendDiagnostic(data []byte) {
	defer m.clampViewports()
	m.diagnostics.viewport.appended(bytes.Count(data, []byte{'\n'}))
	m.diagnostics.data = append(m.diagnostics.data, data...)
	if len(m.diagnostics.data) <= maxDiagnosticBytes {
		return
	}
	m.diagnostics.data = append([]byte(nil), m.diagnostics.data[len(m.diagnostics.data)-maxDiagnosticBytes:]...)
	m.diagnostics.truncated = true
}

func (m *tuiModel) appendLog(id string, data []byte, ingressTruncated bool) {
	defer m.clampViewports()
	log := m.logs[id]
	if log == nil {
		log = m.newStepLog(id)
		m.logs[id] = log
	}
	if ingressTruncated {
		// Never discard a gap marker while keeping bytes on both sides of it:
		// that could turn incomplete output into a misleading complete line.
		if evicted := log.groups.makeRoomForGap(); evicted > 0 {
			log.data = append([]byte(nil), log.data[evicted:]...)
			log.groups.update(log.data, evicted)
			log.truncated = true
		}
	}
	beforeLines := bytes.Count([]byte(log.groups.render(log.data)), []byte{'\n'})
	if ingressTruncated {
		log.groups.gap(len(log.data))
	}
	log.data = append(log.data, data...)
	log.groups.update(log.data, 0)
	afterLines := bytes.Count([]byte(log.groups.render(log.data)), []byte{'\n'})
	log.viewport.appended(max(afterLines-beforeLines, 0))
	oldLen := len(log.data)
	defer func() { log.groups.update(log.data, oldLen-len(log.data)) }()

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

	if m.showDiagnostics {
		writeViewLine(&b, m.width, "Diagnostics")
		if m.diagnostics.truncated {
			writeViewLine(&b, m.width, "[earlier diagnostics truncated]")
		}
		lines := m.diagnostics.viewport.visible(m.diagnostics.lines(), m.logHeight())
		for _, line := range lines {
			writeViewLine(&b, m.width, line)
		}
		if m.confirming {
			writeViewLine(&b, m.width, "Cancel this command and its running workflow? (y/N)")
		} else {
			writeViewLine(&b, m.width, "d steps • q quit • PgUp/PgDn scroll • f tail")
		}
		view := tea.NewView(b.String())
		view.AltScreen = true
		return view
	}

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
		writeViewLine(&b, m.width, fmt.Sprintf("%s%-10s %s%s", cursor, state, step.Name, m.timings[step.ID].label(m.now)))
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
			lines := log.viewport.visible(log.lines(), m.logHeight())
			for _, line := range lines {
				writeViewLine(&b, m.width, line)
			}
		}
	}
	if m.confirming {
		writeViewLine(&b, m.width, "Cancel this command and its running workflow? (y/N)")
	} else {
		writeViewLine(&b, m.width, "↑/↓ select • q quit • d logs • PgUp/PgDn scroll • f tail • c collapse")
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
