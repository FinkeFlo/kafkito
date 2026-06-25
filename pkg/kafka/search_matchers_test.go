// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package kafka

import (
	"testing"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSMatcher_ReusesRuntimeAcrossMessages(t *testing.T) {
	t.Parallel()

	m, err := newJSMatcher(`value.includes("yes")`)
	require.NoError(t, err)

	rtBefore := m.rt
	require.NotNil(t, rtBefore, "runtime must be built once at construction")

	hit, err := m.match(&Message{Value: "say yes"})
	require.NoError(t, err)
	assert.True(t, hit)

	miss, err := m.match(&Message{Value: "say no"})
	require.NoError(t, err)
	assert.False(t, miss)

	assert.Same(t, rtBefore, m.rt, "runtime must be reused, not reallocated per message")
}

func TestJSMatcher_RecoversAfterTimeout(t *testing.T) {
	t.Parallel()

	// Loops only for value "loop"; any other value returns quickly. The leading
	// return surfaces the IIFE result to the wrapper so the match value is real.
	m, err := newJSMatcher(`return (function(){ if (value === "loop") { while(true){} } return value === "ok"; })();`)
	require.NoError(t, err)

	// First call hits the per-call interrupt and errors.
	_, err = m.match(&Message{Value: "loop"})
	assert.Error(t, err, "runaway script must be interrupted")

	// SAME runtime, normal input: the matcher must reuse the interrupted runtime
	// and complete normally on the next message.
	got, err := m.match(&Message{Value: "ok"})
	require.NoError(t, err, "reused runtime must recover after a prior interrupt")
	assert.True(t, got)

	// And a non-matching value still evaluates without error.
	got, err = m.match(&Message{Value: "no"})
	require.NoError(t, err)
	assert.False(t, got)
}

// TestJSMatcher_StaleInterruptClearedAtCallStart proves that an interrupt flag
// already set on the runtime (e.g. left by the watchdog goroutine winning the
// race after a fast previous call) does NOT abort the current match invocation.
// Without the start-of-call ClearInterrupt in match, the call below would
// return an error; with it the call succeeds.
func TestJSMatcher_StaleInterruptClearedAtCallStart(t *testing.T) {
	t.Parallel()

	m, err := newJSMatcher(`value === "hello"`)
	require.NoError(t, err)

	// Simulate the race: set a stale interrupt on the runtime directly,
	// as would happen if the watchdog goroutine fired after a fast script
	// returned but before close(done) was selected.
	m.rt.Interrupt("stale interrupt from previous call")

	// match must clear the stale interrupt at the start and succeed.
	got, err := m.match(&Message{Value: "hello"})
	require.NoError(t, err, "start-of-call ClearInterrupt must discard the stale interrupt")
	assert.True(t, got)
}

// TestJSMatcher_GlobalStatePersistsAcrossCalls documents that explicit
// globalThis mutations accumulate across match invocations because the goja
// runtime is intentionally reused for performance. This is NOT a supported use
// case: filter expressions must be side-effect-free (see jsMatcher doc comment).
// The test exists to make the behaviour observable and intentional, not to
// endorse it.
func TestJSMatcher_GlobalStatePersistsAcrossCalls(t *testing.T) {
	t.Parallel()

	// Each call increments a global counter and returns the count.
	// A purely stateless filter must never do this; we are documenting what
	// happens when the contract is violated, not prescribing it.
	m, err := newJSMatcher(`(globalThis._n = (globalThis._n || 0) + 1) > 0`)
	require.NoError(t, err)

	// First call: _n becomes 1 (> 0 == true).
	got1, err := m.match(&Message{})
	require.NoError(t, err)
	assert.True(t, got1, "first call: global initialised to 1")

	// Second call: _n becomes 2 (> 0 == true).
	got2, err := m.match(&Message{})
	require.NoError(t, err)
	assert.True(t, got2, "second call: global incremented to 2")

	// The counter is accessible from the runtime directly, confirming the
	// global persisted across both calls.
	nVal := m.rt.Get("_n")
	require.NotNil(t, nVal, "global _n must still exist on the reused runtime")
	assert.Equal(t, int64(2), nVal.Export(), "global _n must equal 2 after two calls")
}

// TestJSMatcher_ClearInterruptGuardsTimerRace pins down why match defers
// j.rt.ClearInterrupt(). goja consumes an interrupt that fires *during*
// execution, so a mid-loop timeout self-clears (see RecoversAfterTimeout).
// The dangerous case is the timer firing just *after* a fast script returns:
// the interrupt flag is then set on an idle runtime and would abort the very
// next Run* call. This test reproduces that race deterministically at the goja
// level and proves ClearInterrupt is the thing that lets the runtime be reused.
func TestJSMatcher_ClearInterruptGuardsTimerRace(t *testing.T) {
	t.Parallel()

	rt := goja.New()
	prog, err := goja.Compile("kafkito-filter.js", "(function(){ return true; })", true)
	require.NoError(t, err)
	v, err := rt.RunProgram(prog)
	require.NoError(t, err)
	fn, ok := goja.AssertFunction(v)
	require.True(t, ok)

	// Interrupt fires while the runtime is idle (the script already returned),
	// exactly like the per-call timer winning the race after a fast match.
	rt.Interrupt("late timeout")
	_, err = fn(goja.Undefined())
	require.Error(t, err, "a stale interrupt must abort the next call without ClearInterrupt")

	// This is what match's defer does: clearing the flag lets the runtime serve
	// the next message normally.
	rt.Interrupt("late timeout again")
	rt.ClearInterrupt()
	res, err := fn(goja.Undefined())
	require.NoError(t, err, "ClearInterrupt must reset the runtime for reuse")
	assert.True(t, res.ToBoolean())
}
