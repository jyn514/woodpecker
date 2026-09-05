// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package logger

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputRouterCapturesAndRestoresWithoutReplacingLogger(t *testing.T) {
	var base, first, second bytes.Buffer
	router := new(outputRouter)
	router.setBase(&base)

	restoreFirst := router.capture(&first, true)
	restoreSecond := router.capture(&second, false)
	_, err := router.Write([]byte("captured"))
	require.NoError(t, err)
	assert.Empty(t, base.String())
	assert.Equal(t, "captured", first.String())
	assert.Equal(t, "captured", second.String())

	restoreFirst()
	_, err = router.Write([]byte(" shared"))
	require.NoError(t, err)
	assert.Equal(t, " shared", base.String())
	assert.Equal(t, "captured", first.String())
	assert.Equal(t, "captured shared", second.String())

	restoreSecond()
	restoreSecond()
	_, err = router.Write([]byte(" base"))
	require.NoError(t, err)
	assert.Equal(t, " shared base", base.String())
}
