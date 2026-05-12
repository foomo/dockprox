package upstream_test

import (
	"fmt"

	"github.com/foomo/dockprox/pkg/upstream"
)

func ExampleNewDirect() {
	d := upstream.NewDirect("bypass")
	fmt.Println(d.Name())
	// Output: bypass
}
