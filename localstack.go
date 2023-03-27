package localstack

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const FixedPort = "4566/tcp"
const LocalStackImage = "localstack/localstack:1.4"

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
	waitForInit         bool
}

// New returns the current stack instance
func New() *Stack {
	return &Stack{
		ctx:         context.TODO(),
		pm:          nat.PortMap{},
		waitForInit: true,
	}
}

// Start starts the stack instance with options, forces a restart if required
func (s *Stack) Start(forceRestart bool, opts ...StackOption) error {
	s.Lock()
	defer s.Unlock()
	if s.started {
		if !forceRestart {
			return nil
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

func (s *Stack) start() error {

	go func() {
		<-s.ctx.Done()
		fmt.Printf("Stopping Container: %s\n", s.containerID)
		if err := s.stop(); err != nil {
			panic(err)
		}
	}()

	s.pm[nat.Port(FixedPort)] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: ""}}

	if err := s.ensureImage(LocalStackImage); err != nil {
		return err
	}

	resp, err := s.cli.ContainerCreate(s.ctx,
		&container.Config{
			Image:        LocalStackImage,
			Tty:          true,
			AttachStdout: true,
			AttachStderr: true,
		}, &container.HostConfig{
			PortBindings: s.pm,
			Mounts:       s.getVolumeMounts(),
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
	if s.waitForInit {
		for {
			if s.initTimeout > 0 && time.Since(start) > time.Duration(s.initTimeout)*time.Second {
				_ = fmt.Errorf("localstack: init timeout exceeded (%d seconds)", s.initTimeout)
			}
			if s.initComplete() {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	cont, err := s.cli.ContainerInspect(s.ctx, s.containerID)
	if err != nil {
		return err
	}
	ports := cont.NetworkSettings.Ports
	bindings := ports[nat.Port(FixedPort)]
	s.pm[nat.Port(FixedPort)] = []nat.PortBinding{{HostIP: "localhost", HostPort: bindings[0].HostPort}}

	s.started = true
	return nil
}

func (s *Stack) ensureImage(imageName string) error {

	images, err := s.cli.ImageList(s.ctx, types.ImageListOptions{All: true})
	if err != nil {
		return err
	}

	for _, image := range images {
		if image.ID == imageName {
			return nil
		}
	}

	resp, err := s.cli.ImagePull(s.ctx, imageName, types.ImagePullOptions{})
	if err != nil {
		return err
	}

	defer func() { _ = resp.Close() }()
	_, err = io.Copy(ioutil.Discard, resp)
	return err
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
		logLineCheck = "INFO success: infra entered RUNNING state"
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
	for _, cont := range containers {
		if cont.Image == LocalStackImage {
			s.containerID = cont.ID
			return true
		}
	}
	return false
}

func (s *Stack) isFunctional() bool {
	if !s.started {
		return false
	}

	cfg, err := s.createTestConfig()
	if err != nil {
		return false
	}
	api := sqs.NewFromConfig(cfg)
	queueUrl, err := api.CreateQueue(s.ctx, &sqs.CreateQueueInput{QueueName: aws.String("test-queue")})
	if err != nil || queueUrl.QueueUrl == nil {
		return false
	}
	_, _ = api.DeleteQueue(s.ctx, &sqs.DeleteQueueInput{QueueUrl: queueUrl.QueueUrl})
	return true
}

func (s *Stack) createTestConfig() (aws.Config, error) {
	return config.LoadDefaultConfig(s.ctx,
		config.WithRegion("us-east-1"),
		config.WithEndpointResolverWithOptions(aws.EndpointResolverWithOptionsFunc(func(_, _ string, _ ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				PartitionID:       "aws",
				URL:               s.EndpointURL(),
				SigningRegion:     "us-east-1",
				HostnameImmutable: true,
			}, nil
		})),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("dummy", "dummy", "dummy")),
	)
}
