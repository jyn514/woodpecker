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

type tuiSession struct {
	input   io.Reader
	output  io.Writer
	program *tea.Program
	mu      sync.Mutex
	cancel  context.CancelFunc
	closed  bool
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
	return &tuiSession{input: input, output: output}
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
func (s *tuiSession) Submit(event execEvent)          { s.send(eventMsg{event: event}) }
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
	model := newTUIModel(s.cancelWorkflow)
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
