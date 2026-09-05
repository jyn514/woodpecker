// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/multierr"
	"golang.org/x/term"

	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/tracing"
)

type ingressLog struct {
	data      []byte
	pending   bool
	truncated bool
}

type tuiSession struct {
	input   io.Reader
	output  io.Writer
	program *tea.Program
	mu      sync.Mutex
	cancel  context.CancelFunc
	closed  bool
	ingress map[string]*ingressLog
}

func validateTUITerminals(input, output *os.File) error {
	if !term.IsTerminal(int(input.Fd())) {
		return fmt.Errorf("--tui requires terminal input")
	}
	if !term.IsTerminal(int(output.Fd())) {
		return fmt.Errorf("--tui requires terminal output")
	}
	return nil
}

func newTUISession(input io.Reader, output io.Writer) *tuiSession {
	return &tuiSession{input: input, output: output, ingress: map[string]*ingressLog{}}
}
func (*tuiSession) Print(outputStream, string) {}
func (s *tuiSession) send(msg tea.Msg) {
	s.mu.Lock()
	p, closed := s.program, s.closed
	s.mu.Unlock()
	if p != nil && !closed {
		p.Send(msg)
	}
}
func (s *tuiSession) BeginWorkflow(w execWorkflow) { s.send(workflowStartedMsg(w)) }
func (s *tuiSession) FinishWorkflow(id string, err error) {
	s.send(workflowFinishedMsg{id: id, err: err})
}
func (s *tuiSession) Submit(event execEvent) {
	fragment, ok := event.(stepLogFragment)
	if !ok {
		s.send(eventMsg{event: event})
		return
	}
	key := fragment.WorkflowID + "\x00" + fragment.StepID
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	entry := s.ingress[key]
	if entry == nil {
		entry = &ingressLog{}
		s.ingress[key] = entry
	}
	entry.data = append(entry.data, fragment.Data...)
	if len(entry.data) > maxPendingLogBytes {
		entry.data = append([]byte(nil), entry.data[len(entry.data)-maxPendingLogBytes:]...)
		entry.truncated = true
	}
	notify := !entry.pending
	entry.pending = true
	s.mu.Unlock()
	if notify {
		s.send(logReadyMsg{workflowID: fragment.WorkflowID, stepID: fragment.StepID})
	}
}
func (s *tuiSession) drainLog(workflowID, stepID string) ([]byte, bool) {
	key := workflowID + "\x00" + stepID
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.ingress[key]
	if entry == nil {
		return nil, false
	}
	data, truncated := entry.data, entry.truncated
	entry.data, entry.truncated, entry.pending = nil, false, false
	return data, truncated
}
func (s *tuiSession) Logger(id string) logging.Logger { return eventLogger(id, s.Submit) }
func (s *tuiSession) Tracer(id string) tracing.Tracer { return eventTracer(id, s.Submit) }
func (s *tuiSession) cancelWorkflow() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *tuiSession) ExecuteWorkflow(parent context.Context, _ execWorkflow, execute func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.cancel = nil; s.mu.Unlock() }()
	return execute(ctx)
}
func (s *tuiSession) Run(ctx context.Context, execute func(context.Context) error) error {
	model := newTUIModel(s.cancelWorkflow, s.drainLog)
	p := tea.NewProgram(model, tea.WithInput(s.input), tea.WithOutput(s.output), tea.WithoutSignals())
	s.mu.Lock()
	s.program = p
	s.mu.Unlock()
	result := make(chan error, 1)
	go func() { err := execute(ctx); result <- err; s.send(commandFinishedMsg{err: err}) }()
	_, uiErr := p.Run()
	execErr := <-result
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return multierr.Combine(execErr, uiErr)
}
func (s *tuiSession) Close() error { s.mu.Lock(); s.closed = true; s.mu.Unlock(); return nil }
