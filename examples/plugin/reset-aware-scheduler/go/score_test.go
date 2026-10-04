package main

import (
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var now = time.Unix(1_790_000_000, 0)

const day = 24 * time.Hour

func claude(id string, used float64, resetIn time.Duration) pluginapi.SchedulerAuthCandidate {
	return pluginapi.SchedulerAuthCandidate{ID: id, Provider: "claude", Quota: &pluginapi.SchedulerQuotaObservation{
		ObservedAt: now.Add(-time.Minute),
		Signals: map[string]string{
			"Anthropic-Ratelimit-Unified-7d-Utilization": strconv.FormatFloat(used, 'f', -1, 64),
			"Anthropic-Ratelimit-Unified-7d-Reset":       strconv.FormatInt(now.Add(resetIn).Unix(), 10),
		},
	}}
}

// shares runs pick over an evenly spaced grid of draws and returns the
// fraction of picks each candidate received.
func shares(candidates []pluginapi.SchedulerAuthCandidate) map[string]float64 {
	const n = 10000
	counts := map[string]int{}
	for k := 0; k < n; k++ {
		counts[pick(candidates, now, defaultExponent, (float64(k)+0.5)/n)]++
	}
	out := map[string]float64{}
	for id, count := range counts {
		out[id] = float64(count) / n
	}
	return out
}

func TestSimilarResetDistancesShareTrafficRoughlyEvenly(t *testing.T) {
	got := shares([]pluginapi.SchedulerAuthCandidate{
		claude("a", 0, 4*day), claude("b", 0, 4*day), claude("c", 0, 5*day),
	})
	t.Logf("shares = %v", got)
	if got["c"] < 0.15 || got["a"] > 0.45 {
		t.Fatalf("shares = %v, want 4/4/5 days to split without starving c", got)
	}
}

func TestAccountNearResetWithQuotaLeftIsHammered(t *testing.T) {
	got := shares([]pluginapi.SchedulerAuthCandidate{
		claude("a", 0, 4*day), claude("b", 0, 4*day), claude("c", 0, 5*day), claude("soon", 0.65, 1*day),
	})
	t.Logf("shares = %v", got)
	if got["soon"] < 0.85 {
		t.Fatalf("shares = %v, want the 1-day account with 35%% left to take most traffic", got)
	}
}

func TestExhaustedAccountsAreNeverPicked(t *testing.T) {
	rejected := claude("rejected", 0.4, 1*day)
	rejected.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Status"] = "rejected"
	got := shares([]pluginapi.SchedulerAuthCandidate{claude("spent", 1, 1*day), rejected, claude("ok", 0.9, 6*day)})
	if got["ok"] != 1 {
		t.Fatalf("shares = %v, want only the account with quota left", got)
	}
	if id := pick([]pluginapi.SchedulerAuthCandidate{claude("spent", 1, 1*day)}, now, defaultExponent, 0.5); id != "" {
		t.Fatalf("pick() = %q, want no decision when every account is exhausted", id)
	}
}

func TestSnapshotFromBeforeTheResetCountsAsFullWeek(t *testing.T) {
	got := weeklyWindowOf(claude("reset", 1, -time.Hour).Quota, now)
	if got != (weeklyWindow{remaining: 1, timeToReset: week}) {
		t.Fatalf("weeklyWindowOf() = %+v, want a fresh week after the reset passed", got)
	}
}

func TestCodexWeeklyWindowIsFoundByLength(t *testing.T) {
	observedAt := now.Add(-time.Hour)
	quota := &pluginapi.SchedulerQuotaObservation{ObservedAt: observedAt, Signals: map[string]string{
		"X-Codex-Primary-Used-Percent":          "90",
		"X-Codex-Primary-Window-Minutes":        "300",
		"X-Codex-Primary-Reset-After-Seconds":   "600",
		"X-Codex-Secondary-Used-Percent":        "25",
		"X-Codex-Secondary-Window-Minutes":      "10080",
		"X-Codex-Secondary-Reset-After-Seconds": strconv.Itoa(int((2 * day).Seconds())),
	}}
	got := weeklyWindowOf(quota, now)
	if got.remaining != 0.75 || got.timeToReset != 2*day-time.Hour {
		t.Fatalf("weeklyWindowOf() = %+v, want 75%% left resetting in 47h", got)
	}

	weeklyPrimary := &pluginapi.SchedulerQuotaObservation{ObservedAt: observedAt, Signals: map[string]string{
		"X-Codex-Primary-Used-Percent":   "2",
		"X-Codex-Primary-Window-Minutes": "10080",
		"X-Codex-Primary-Reset-At":       strconv.FormatInt(now.Add(3*day).Unix(), 10),
	}}
	got = weeklyWindowOf(weeklyPrimary, now)
	if got.remaining != 0.98 || got.timeToReset != 3*day {
		t.Fatalf("weeklyWindowOf() = %+v, want weekly primary window", got)
	}
}
