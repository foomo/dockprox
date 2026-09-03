package menubar

import (
	"context"
	"net"
	"sync"
	"time"
)

// ProbeTimeout bounds a single forward endpoint probe. Forwards point at
// local ports or nearby ingresses, so a reachable one answers in
// milliseconds; this only caps how long an unreachable or blackholed
// address delays the menu.
const ProbeTimeout = 500 * time.Millisecond

// ProbeForwards reports which forwards accept a TCP connection, keyed by
// upstream name. Every forward is probed concurrently, so the whole call
// costs one ProbeTimeout rather than one per endpoint.
//
// A successful dial is the only signal available without speaking the
// backend's protocol: it proves something is listening, not that it is
// healthy. The connection is closed immediately.
func ProbeForwards(ctx context.Context, forwards []ForwardStatus) map[string]bool {
	out := make(map[string]bool, len(forwards))

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	for _, f := range forwards {
		wg.Add(1)

		go func() {
			defer wg.Done()

			up := probeAddr(ctx, f.Addr)

			mu.Lock()
			defer mu.Unlock()

			out[f.Name] = up
		}()
	}

	wg.Wait()

	return out
}

// probeAddr dials addr and reports whether the connection was accepted.
func probeAddr(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	var d net.Dialer

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}
