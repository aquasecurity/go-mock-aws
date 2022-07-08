package localstack

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_TestLocalStack(t *testing.T) {

	ensureNoLocalStack(t)

	stack := Get()
	assert.NotNil(t, stack)

	err := stack.Start(false)
	fmt.Println("Endpoint url: " + stack.EndpointURL())
	assert.True(t, stack.isFunctional())
	require.NoError(t, err)

	err = stack.Stop()
	require.NoError(t, err)

	assert.False(t, stack.isFunctional())
}

func ensureNoLocalStack(t *testing.T) {

	cli, err := client.NewClientWithOpts(client.FromEnv)
	require.NoError(t, err)

	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{})
	require.NoError(t, err)

	for _, c := range containers {
		if c.Image == "localstack/localstack:latest" {
			err = cli.ContainerRemove(context.Background(), c.ID, types.ContainerRemoveOptions{
				RemoveVolumes: true,
				Force:         true,
			})
			require.NoError(t, err)
		}
	}

}
