// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainSessionKeepsStreamsSeparate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	s := newPlainSession(sessionWriters{stdout: &stdout, stderr: &stderr})
	s.Print(outputStdout, "heading\n")
	s.Print(outputStderr, "log\n")
	assert.Equal(t, "heading\n", stdout.String())
	assert.Equal(t, "log\n", stderr.String())
}
