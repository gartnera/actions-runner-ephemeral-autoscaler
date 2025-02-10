package common

import (
	"context"
	"testing"

	"gopkg.in/stretchr/testify.v1/require"
)

const cloudInitOverlay = `
packages:
  - gcc
runcmd:
  - id
`

func TestCloudInitPrepare(t *testing.T) {
	cloudInitPrepare, err := GetCloudInitPrepare(context.Background(), cloudInitOverlay)
	require.NoError(t, err)
	require.Contains(t, cloudInitPrepare, "docker-ce")
	require.Contains(t, cloudInitPrepare, "gcc")
	require.Contains(t, cloudInitPrepare, "id")
}
