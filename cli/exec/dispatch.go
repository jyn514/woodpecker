// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	"go.woodpecker-ci.org/woodpecker/v3/cli/common"
)

func dispatch(ctx context.Context, c *cli.Command, session execSession) error {
	if c.Args().Len() == 0 {
		isDir, path, err := common.DetectPipelineConfig()
		if err != nil {
			return err
		}
		if isDir {
			return execDir(ctx, c, session, path)
		}
		return execFile(ctx, c, session, path)
	}
	multi := c.Args().Len() > 1
	for _, path := range c.Args().Slice() {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if multi {
			session.Print(outputStdout, fmt.Sprintf("# %s\n", info.Name()))
		}
		if info.IsDir() {
			err = execDir(ctx, c, session, path)
		} else {
			err = execFile(ctx, c, session, path)
		}
		if err != nil {
			return err
		}
		if multi {
			session.Print(outputStdout, "\n")
		}
	}
	return nil
}
