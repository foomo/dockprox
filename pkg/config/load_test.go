package config_test

import (
	"bytes"
	"fmt"

	"github.com/foomo/dockprox/pkg/config"
)

func ExampleLoad() {
	r := bytes.NewReader([]byte(`
listen: 127.0.0.1:8888
upstreams:
  jump: { type: socks5, addr: 127.0.0.1:1080 }
rules:
  - match: "*.azurecr.io"
    upstream: jump
`))

	c, err := config.Load(r)
	if err != nil {
		fmt.Println("load:", err)
		return
	}

	fmt.Println(c.Listen, len(c.Rules))
	// Output: 127.0.0.1:8888 1
}
