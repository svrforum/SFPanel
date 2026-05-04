package compose

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeDiff_ImageChange(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1.24
    ports:
      - "8080:80"
`
	proposed := `services:
  web:
    image: nginx:1.25
    ports:
      - "8080:80"
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)

	require.Equal(t, 0, got.Summary.Added)
	require.Equal(t, 1, got.Summary.Modified)
	require.Equal(t, 0, got.Summary.Removed)

	images, ok := got.ByCategory["image"].([]ImageChange)
	require.True(t, ok, "ByCategory[image] should be []ImageChange")
	require.Len(t, images, 1)
	require.Equal(t, "web", images[0].Service)
	require.Equal(t, "nginx:1.24", images[0].From)
	require.Equal(t, "nginx:1.25", images[0].To)
}
