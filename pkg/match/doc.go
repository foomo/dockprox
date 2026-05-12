// Package match compiles a list of host patterns (exact or *.suffix) into
// a fast lookup table. The first matching rule wins; rule order is
// preserved from the configuration so users can express priority.
//
// The matcher is built once at startup and is safe for concurrent use.
package match
