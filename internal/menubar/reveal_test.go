//go:build darwin

package menubar //nolint:testpackage // needs access to unexported buildRevealCommand

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildRevealCommand(t *testing.T) {
	cmd := buildRevealCommand(t.Context(), "/some/path/config.yaml")
	assert.Equal(t, "open", cmd.Args[0])
	assert.Equal(t, []string{"open", "-R", "/some/path/config.yaml"}, cmd.Args)
}
