package match

import (
	"strings"

	"github.com/pkg/errors"
)

// Rule pairs a host pattern with the upstream that should handle requests
// for that host. Pattern is either an exact lowercase hostname (e.g.
// "ghcr.io") or a leading-wildcard suffix (e.g. "*.azurecr.io").
type Rule struct {
	Pattern  string
	Upstream string
}

type suffixRule struct {
	suffix  string // ".azurecr.io"
	ruleIdx int
}

// Matcher resolves a host to the first matching Rule.
type Matcher struct {
	rules  []Rule
	exact  map[string]int
	suffix []suffixRule
}

// New compiles rules into a Matcher. knownUpstreams is the set of upstream
// names defined in the configuration; rules referencing an unknown upstream
// cause New to return an error so config mistakes surface at startup.
func New(rules []Rule, knownUpstreams map[string]struct{}) (*Matcher, error) {
	m := &Matcher{
		rules: make([]Rule, len(rules)),
		exact: make(map[string]int),
	}
	for i, r := range rules {
		if err := validatePattern(r.Pattern); err != nil {
			return nil, errors.Wrapf(err, "rule %d", i)
		}

		if _, ok := knownUpstreams[r.Upstream]; !ok {
			return nil, errors.Errorf("rule %d: unknown upstream %q", i, r.Upstream)
		}

		p := strings.ToLower(r.Pattern)

		m.rules[i] = r
		if strings.HasPrefix(p, "*.") {
			m.suffix = append(m.suffix, suffixRule{suffix: p[1:], ruleIdx: i})
		} else {
			if _, dup := m.exact[p]; !dup {
				m.exact[p] = i
			}
		}
	}

	return m, nil
}

// First returns the first Rule that matches host (case-insensitive, port
// stripped) and true. If no rule matches, it returns nil and false.
func (m *Matcher) First(host string) (*Rule, bool) {
	h := strings.ToLower(host)
	h = stripPort(h)

	bestIdx := -1
	if i, ok := m.exact[h]; ok {
		bestIdx = i
	}

	for _, s := range m.suffix {
		if !strings.HasSuffix(h, s.suffix) {
			continue
		}

		if bestIdx == -1 || s.ruleIdx < bestIdx {
			bestIdx = s.ruleIdx
		}
	}

	if bestIdx == -1 {
		return nil, false
	}

	return &m.rules[bestIdx], true
}

// stripPort removes a trailing ":port" from host, correctly handling
// bracketed IPv6 literals such as "[::1]:443".
func stripPort(host string) string {
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			return host[1:end]
		}

		return host
	}

	if i := strings.LastIndex(host, ":"); i > 0 {
		return host[:i]
	}

	return host
}

func validatePattern(p string) error {
	if p == "" {
		return errors.New("empty pattern")
	}

	stars := strings.Count(p, "*")
	if stars == 0 {
		return nil
	}

	if stars > 1 {
		return errors.New("multiple wildcards not allowed")
	}

	if !strings.HasPrefix(p, "*.") {
		return errors.New("wildcard must be leading \"*.\"")
	}

	return nil
}
