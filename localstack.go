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

const FixedPort = "4566/tcp"

type Stack struct {
	sync.RWMutex
	initScriptDir       string
	started             bool
	reuseExisting       bool
	containerName       string
	ctx                 context.Context
	cli                 *client.Client
	volumeMounts        map[string]string
	initCompleteLogLine string
	containerID         string
	initTimeout         int
	pm                  nat.PortMap
}

var stack = &Stack{
	ctx: context.Background(),
	pm:  nat.PortMap{},
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
	return s.stop()
}

func (s *Stack) stop() error {
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

func (s *Stack) EndpointURL() string {

	if s.containerID != "" {
		if port, ok := s.pm[nat.Port(FixedPort)]; ok {
			return "http://" + port[0].HostIP + ":" + port[0].HostPort
		}

	}
	return ""
}

func (s *Stack) start(forceRestart bool) error {
	if s.instanceAlreadyRunning() {
		if !forceRestart {
			return nil
		}
		if err := s.stop(); err != nil {
			return err
		}
	}

	s.pm[nat.Port(FixedPort)] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: ""}}

	resp, err := s.cli.ContainerCreate(s.ctx,
		&container.Config{
			Image:        "localstack/localstack:latest",
			Tty:          true,
			AttachStdout: true,
			AttachStderr: true,
		}, &container.HostConfig{
			PortBindings: s.pm,
			Mounts:       stack.getVolumeMounts(),
			AutoRemove:   true,
		}, nil, nil, s.containerName)
	if err != nil {
		if s.reuseExisting && strings.Contains(err.Error(), fmt.Sprintf("The container name \"%s\" is already in use by container", s.containerName)) {
			return nil
		}
		return fmt.Errorf("localstack: could not create container: %w", err)
	}

	s.containerID = resp.ID

	if err := s.cli.ContainerStart(s.ctx, s.containerID, types.ContainerStartOptions{}); err != nil {
		return err
	}

	start := time.Now()
	for {
		if s.initTimeout > 0 && time.Since(start) > time.Duration(s.initTimeout)*time.Second {
			_ = fmt.Errorf("localstack: init timeout exceeded (%d seconds)", s.initTimeout)
		}
		if s.initComplete() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	s.started = true
	return nil
}

func (s *Stack) initComplete() bool {
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

func (s *Stack) instanceAlreadyRunning() bool {
	containers, err := s.cli.ContainerList(s.ctx, types.ContainerListOptions{})
	if err != nil {
		return false
	}
	for _, container := range containers {
		if container.Image == "localstack/localstack:latest" {
			s.containerID = container.ID
			return true
		}
	}
	return false
}
