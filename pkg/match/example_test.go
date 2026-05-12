package match_test

import (
	"fmt"

	"github.com/foomo/dockprox/pkg/match"
)

func ExampleMatcher_First() {
	m, _ := match.New([]match.Rule{
		{Pattern: "*.azurecr.io", Upstream: "jump"},
		{Pattern: "ghcr.io", Upstream: "jump"},
	}, map[string]struct{}{"jump": {}})

	r, ok := m.First("foo.azurecr.io:443")
	fmt.Println(ok, r.Pattern, r.Upstream)
	// Output: true *.azurecr.io jump
}
