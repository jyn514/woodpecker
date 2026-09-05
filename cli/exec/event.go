// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"fmt"

	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
)

type execWorkflow struct {
	ID     string
	Name   string
	Stages []execStage
}

type execStage struct {
	Name  string
	Steps []execStep
}

type execStep struct {
	ID       string
	Name     string
	Detached bool
}

type execStepState uint8

const (
	stepPending execStepState = iota
	stepRunning
	stepSuccessful
	stepFailed
	stepCanceled
	stepSkipped
)

func (s execStepState) String() string {
	return [...]string{"pending", "running", "successful", "failed", "canceled", "skipped"}[s]
}

type execEvent interface{ isExecEvent() }

type stepLogFragment struct {
	WorkflowID string
	StepID     string
	Data       []byte
}

func (stepLogFragment) isExecEvent() {}

type stepStateChanged struct {
	WorkflowID string
	StepID     string
	State      execStepState
}

func (stepStateChanged) isExecEvent() {}

type diagnosticFragment struct {
	Data []byte
}

func (diagnosticFragment) isExecEvent() {}

func workflowDescriptor(name string, config *backend_types.Config) execWorkflow {
	w := execWorkflow{ID: name, Name: name, Stages: make([]execStage, 0, len(config.Stages))}
	for i, stage := range config.Stages {
		s := execStage{Name: fmt.Sprintf("Stage %d", i+1), Steps: make([]execStep, 0, len(stage.Steps))}
		for _, step := range stage.Steps {
			s.Steps = append(s.Steps, execStep{ID: step.UUID, Name: step.Name, Detached: step.Detached})
		}
		w.Stages = append(w.Stages, s)
	}
	return w
}
