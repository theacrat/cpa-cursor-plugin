package cursor

import (
	"testing"
	"time"
)

func TestLiveProgressBudgetsFollowInitialAndResumedPhases(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, test := range []struct {
		name                      string
		resumed                   bool
		before, thinking, visible time.Duration
	}{
		{"initial", false, 180 * time.Second, 45 * time.Second, 45 * time.Second},
		{"resumed", true, 90 * time.Second, 180 * time.Second, 45 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			progress := newLiveProgress(now, test.resumed)
			if progress.budget() != test.before || progress.expired(now.Add(test.before-time.Nanosecond)) || !progress.expired(now.Add(test.before)) {
				t.Fatal("pre-output phase has the wrong deadline")
			}
			thinkingAt := now.Add(time.Second)
			progress.observe(thinkingAt, "thinking", true)
			if progress.budget() != test.thinking || progress.expired(thinkingAt.Add(test.thinking-time.Nanosecond)) || !progress.expired(thinkingAt.Add(test.thinking)) {
				t.Fatal("reasoning-only phase has the wrong deadline")
			}
			visibleAt := thinkingAt.Add(time.Second)
			progress.observe(visibleAt, "visible", false)
			if progress.budget() != test.visible || progress.expired(visibleAt.Add(test.visible-time.Nanosecond)) || !progress.expired(visibleAt.Add(test.visible)) {
				t.Fatal("visible phase has the wrong deadline")
			}
		})
	}
}

func TestLiveProgressEmptyDeltasDoNotRefreshDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, false)
	progress.observe(now.Add(100*time.Second), "", false)
	progress.observe(now.Add(179*time.Second), "", true)
	if progress.budget() != 180*time.Second || !progress.expired(now.Add(180*time.Second)) {
		t.Fatal("empty or heartbeat-like events refreshed progress")
	}
}

func TestLiveProgressRecoveryIsExpiredOnlyAndBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, true)
	if delay, ok := progress.recover(now); ok || delay != 0 {
		t.Fatal("non-expired run consumed a recovery")
	}
	for index, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		now = now.Add(progress.budget())
		if delay, ok := progress.recover(now); !ok || delay != want {
			t.Fatalf("recovery %d = (%v, %v), want (%v, true)", index+1, delay, ok, want)
		}
		if progress.expired(now) {
			t.Fatal("claimed recovery did not begin a fresh wait window")
		}
	}
	now = now.Add(progress.budget())
	if delay, ok := progress.recover(now); ok || delay != 0 {
		t.Fatal("run exceeded three total recoveries")
	}
}

func TestLiveProgressAllowsOnlyOneRecoveryAfterVisibleText(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, false)
	progress.observe(now, "first answer", false)
	now = now.Add(progress.budget())
	if delay, ok := progress.recover(now); !ok || delay != time.Second {
		t.Fatal("first visible-text recovery was not available")
	}
	progress.observe(now.Add(time.Second), "first answer extended", false)
	now = now.Add(time.Second + progress.budget())
	if delay, ok := progress.recover(now); ok || delay != 0 {
		t.Fatal("new text reset the visible recovery cap")
	}
}

func TestLiveProgressVisibleRecoveryRetainsTotalBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, false)
	for range 2 {
		now = now.Add(progress.budget())
		if _, ok := progress.recover(now); !ok {
			t.Fatal("initial recovery unexpectedly unavailable")
		}
	}
	progress.observe(now, "visible", false)
	now = now.Add(progress.budget())
	if delay, ok := progress.recover(now); !ok || delay != 4*time.Second {
		t.Fatal("visible recovery reset the total recovery backoff")
	}
	progress.observe(now, "visible continued", false)
	if _, ok := progress.recover(now.Add(progress.budget())); ok {
		t.Fatal("total recovery cap was reset after output")
	}
}

func TestLiveProgressRepeatedPrefixesCannotExtendRecoveryForever(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, true)
	progress.observe(now, "reasoning prefix", true)
	for range 3 {
		now = now.Add(progress.budget())
		if _, ok := progress.recover(now); !ok {
			t.Fatal("recovery ended before the configured cap")
		}
		progress.restart(now.Add(time.Second))
		progress.observe(now.Add(2*time.Second), "reasoning ", true)
		progress.observe(now.Add(3*time.Second), "prefix", true)
		if !progress.expired(now.Add(time.Second + progress.budget())) {
			t.Fatal("replayed reasoning prefix refreshed the deadline")
		}
		now = now.Add(time.Second)
	}
	if _, ok := progress.recover(now.Add(progress.budget())); ok {
		t.Fatal("replayed prefix restored an exhausted recovery budget")
	}
}

func TestLiveProgressOnlyBytesBeyondPriorAttemptHighWaterRefreshDeadline(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, false)
	progress.observe(now, "abcdef", false)
	progress.restart(now.Add(time.Second))
	progress.observe(now.Add(10*time.Second), "abc", false)
	progress.observe(now.Add(20*time.Second), "def", false)
	if !progress.expired(now.Add(time.Second + progress.budget())) {
		t.Fatal("chunked repeated visible prefix refreshed the deadline")
	}
	progress.observe(now.Add(30*time.Second), "g", false)
	if progress.expired(now.Add(time.Second+progress.budget())) || !progress.expired(now.Add(30*time.Second+progress.budget())) {
		t.Fatal("new bytes beyond the prefix did not refresh the deadline")
	}
	progress.restart(now.Add(31 * time.Second))
	if progress.budget() != 45*time.Second {
		t.Fatal("restart forgot that visible text was already emitted")
	}
}

func TestLiveProgressThinkingAndVisibleByteHighWaterAreIndependent(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, true)
	progress.observe(now, "long reasoning prefix", true)
	progress.restart(now.Add(time.Second))
	progress.observe(now.Add(10*time.Second), "ok", false)
	if progress.budget() != 45*time.Second || progress.expired(now.Add(50*time.Second)) {
		t.Fatal("reasoning high-water suppressed the first visible text")
	}
	progress.observe(now.Add(20*time.Second), "long reasoning prefix", true)
	if !progress.expired(now.Add(55 * time.Second)) {
		t.Fatal("repeated reasoning refreshed the visible-text deadline")
	}
}

func TestLiveProgressRestartPreservesOriginAndDoesNotRestoreRecoveries(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, true)
	for range 3 {
		now = now.Add(progress.budget())
		if _, ok := progress.recover(now); !ok {
			t.Fatal("recovery unavailable before total cap")
		}
	}
	progress.restart(now.Add(time.Second))
	progress.observe(now.Add(2*time.Second), "new reasoning", true)
	if !progress.start.Equal(time.Unix(1000, 0)) {
		t.Fatal("restart changed the absolute execution origin")
	}
	if _, ok := progress.recover(now.Add(2*time.Second + progress.budget())); ok {
		t.Fatal("restart or fresh reasoning restored exhausted recoveries")
	}
}

func TestLiveProgressTransportAndStallShareOneRecoveryBudget(t *testing.T) {
	now := time.Unix(1000, 0)
	progress := newLiveProgress(now, true)
	if delay, ok := progress.claimRecovery(now); !ok || delay != time.Second {
		t.Fatal("transport recovery did not claim the first attempt")
	}
	now = now.Add(progress.budget())
	if delay, ok := progress.recover(now); !ok || delay != 2*time.Second {
		t.Fatal("stall reset the transport recovery backoff")
	}
	progress.observe(now, "visible", false)
	if delay, ok := progress.claimRecovery(now); !ok || delay != 4*time.Second {
		t.Fatal("transport recovery reset accumulated backoff")
	}
	if _, ok := progress.claimRecovery(now); ok {
		t.Fatal("transport errors exceeded the shared recovery cap")
	}
}
