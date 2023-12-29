package localstack

import (
	"context"
	"fmt"
	"path/filepath"
)

type StackOption func(s *Stack)

// WithInitScriptMount configures the instance with init scripts and waits for a specific line from
// the script to show as ready to continue
func WithInitScriptMount(initScriptDirPath string, completeLogLine string) (StackOption, error) {
	if completeLogLine == "" {
		return nil, fmt.Errorf("init script mount requires a line to wait for in the init script for completion")
	}

	initScriptDirPathAbs, err := filepath.Abs(initScriptDirPath)
	if err != nil {
		return nil, err
	}

	return func(i *Stack) {
		if i.volumeMounts == nil {
			i.volumeMounts = make(map[string]string)
		}
		targetMount := "/etc/localstack/init/ready.d/" + filepath.Base(initScriptDirPath)
		i.volumeMounts[targetMount] = initScriptDirPathAbs
		i.initCompleteLogLine = completeLogLine
	}, err
}

func WithContext(ctx context.Context) StackOption {
	return func(s *Stack) {
		s.ctx = ctx
	}
}

func WithInitTimeout(timeout int) StackOption {
	return func(s *Stack) {
		s.initTimeout = timeout
	}
}

func WithReuseExisting() StackOption {
	return func(s *Stack) {
		s.reuseExisting = true
		s.containerName = "localstack"
	}
}

func WithNotInitWait() StackOption {
	return func(s *Stack) {
		s.waitForInit = false
	}
}
