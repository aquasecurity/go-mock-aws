package localstack

import (
	"context"
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

type Stack struct {
	sync.RWMutex
	initScriptDir       string
	started             bool
	ctx                 context.Context
	cli                 *client.Client
	volumeMounts        map[string]string
	initCompleteLogLine string
	containerID         string
	initTimeout         int
}

var stack = &Stack{
	ctx: context.Background(),
}

// Get returns the current stack instance
func Get() *Stack {
	return stack
}

// Start starts the stack instance with options, forces a restart if required
func (s *Stack) Start(forceRestart bool, opts ...StackOption) error {
	s.Lock()
	defer s.Unlock()
	if s.started {
		if !forceRestart {
			return fmt.Errorf("localstack: already started and restart not requested")
		}
		if err := s.Stop(); err != nil {
			return err
		}
	}
	for _, opt := range opts {
		opt(s)
	}
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}

	s.cli = cli
	return s.start()
}

// Stop stops the stack instance
func (s *Stack) Stop() error {
	s.Lock()
	defer s.Unlock()
	if !s.started || s.containerID == "" {
		return nil
	}
	timeout := time.Second
	if err := s.cli.ContainerStop(context.Background(), s.containerID, &timeout); err != nil {
		return err
	}
	s.containerID = ""
	s.started = false

	return nil
}

func (s *Stack) start() error {
	pm := nat.PortMap{}
	pm[nat.Port("4566/tcp")] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: ""}}

	resp, err := s.cli.ContainerCreate(s.ctx,
		&container.Config{
			Image:        "localstack/localstack:latest",
			Tty:          true,
			AttachStdout: true,
			AttachStderr: true,
		}, &container.HostConfig{
			PortBindings: pm,
			Mounts:       stack.getVolumeMounts(),
			AutoRemove:   true,
		}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("localstack: could not create container: %w", err)
	}

	s.containerID = resp.ID

	start := time.Now()
	for {
		if s.initTimeout > 0 && time.Since(start) > time.Duration(s.initTimeout)*time.Second {
			_ = fmt.Errorf("localstack: init timeout exceeded (%d seconds)", s.initTimeout)
		}
		if s.isInitComplete() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	s.started = true
	return nil
}

func (s *Stack) isInitComplete() bool {
	reader, err := s.cli.ContainerLogs(s.ctx, s.containerID, types.ContainerLogsOptions{
		ShowStdout: true,
		Follow:     false,
	})
	if err != nil {
		return false
	}
	defer func() { _ = reader.Close() }()

	logContent, err := ioutil.ReadAll(reader)
	if err != nil {
		return false
	}

	logLineCheck := s.initCompleteLogLine
	if logLineCheck == "" {
		logLineCheck = "localstack: finished waiting"
	}

	return strings.Contains(string(logContent), logLineCheck)
}

func (s *Stack) getVolumeMounts() []mount.Mount {
	var mounts []mount.Mount
	for mountPath, localPath := range s.volumeMounts {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: localPath,
			Target: mountPath,
		})
	}
	return mounts
}
