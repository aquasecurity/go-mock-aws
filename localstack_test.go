package localstack

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_TestLocalStack(t *testing.T) {
	t.Parallel()
	stack := New()
	assert.NotNil(t, stack)

	err := stack.Start(false)
	require.NoError(t, err)
	fmt.Println("Endpoint url: " + stack.EndpointURL())
	assert.True(t, stack.isFunctional())

	err = stack.Stop()
	require.NoError(t, err)

	assert.False(t, stack.isFunctional())
}

func Test_WithInitScriptMountOption(t *testing.T) {
	t.Parallel()

	initScripts, err := WithInitScriptMount(
		"./init-aws.sh",
		"Bootstrap Complete")
	require.NoError(t, err)

	stack := New()
	assert.NotNil(t, stack)

	err = stack.Start(false, initScripts)
	require.NoError(t, err)
	defer stack.Stop()
}
