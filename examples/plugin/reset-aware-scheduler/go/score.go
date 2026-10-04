package main

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	week = 7 * 24 * time.Hour
	// minTimeToReset caps urgency so an account seconds away from its reset does
	// not get an unbounded weight.
	minTimeToReset = time.Hour
)

// weeklyWindow is a candidate's weekly limit as seen at a point in time.
type weeklyWindow struct {
	remaining   float64 // fraction of the weekly limit left, 0..1
	timeToReset time.Duration
}

// pick returns the ID of a candidate chosen with probability proportional to
// its urgency weight, or "" when no candidate has usable weekly quota left.
// draw is a uniform random number in [0, 1).
func pick(candidates []pluginapi.SchedulerAuthCandidate, now time.Time, exponent, draw float64) string {
	weights := make([]float64, len(candidates))
	total := 0.0
	for i, candidate := range candidates {
		weights[i] = weight(weeklyWindowOf(candidate.Quota, now), exponent)
		total += weights[i]
	}
	if total <= 0 {
		return ""
	}
	target := draw * total
	for i, w := range weights {
		target -= w
		if target < 0 {
			return candidates[i].ID
		}
	}
	return candidates[len(candidates)-1].ID
}

// weight is the remaining fraction scaled by how close the reset is, relative
// to the week: (week / timeToReset)^exponent. With exponent 3, accounts 4 and 5
// days out differ by ~2x, while an account 1 day out is weighted 64x over one
// 4 days out, so it absorbs nearly all traffic until it is drained.
func weight(w weeklyWindow, exponent float64) float64 {
	if w.remaining <= 0 {
		return 0
	}
	t := max(w.timeToReset, minTimeToReset)
	return w.remaining * math.Pow(float64(week)/float64(t), exponent)
}

// weeklyWindowOf reads the weekly window from the latest Claude or Codex quota
// snapshot. Accounts without a snapshot, or whose snapshot predates the reset,
// are treated as full with a whole week to go: lowest urgency, but still picked
// often enough to be observed.
func weeklyWindowOf(quota *pluginapi.SchedulerQuotaObservation, now time.Time) weeklyWindow {
	fresh := weeklyWindow{remaining: 1, timeToReset: week}
	if quota == nil {
		return fresh
	}
	used, resetAt, ok := claudeWeekly(quota.Signals)
	if !ok {
		used, resetAt, ok = codexWeekly(quota.Signals, quota.ObservedAt)
	}
	if !ok || !resetAt.After(now) {
		return fresh
	}
	return weeklyWindow{remaining: min(max(1-used, 0), 1), timeToReset: resetAt.Sub(now)}
}

func claudeWeekly(signals map[string]string) (used float64, resetAt time.Time, ok bool) {
	const prefix = "Anthropic-Ratelimit-Unified-7d-"
	used, okUsed := parseFloat(signals[prefix+"Utilization"])
	resetAt, okReset := parseUnix(signals[prefix+"Reset"])
	if !okUsed || !okReset {
		return 0, time.Time{}, false
	}
	if strings.EqualFold(signals[prefix+"Status"], "rejected") {
		used = 1
	}
	return used, resetAt, true
}

// codexWeekly finds the weekly window by its length: depending on the plan it
// is reported as either the primary or the secondary window.
func codexWeekly(signals map[string]string, observedAt time.Time) (used float64, resetAt time.Time, ok bool) {
	for _, prefix := range []string{"X-Codex-Secondary-", "X-Codex-Primary-"} {
		minutes, okMinutes := parseFloat(signals[prefix+"Window-Minutes"])
		percent, okPercent := parseFloat(signals[prefix+"Used-Percent"])
		if !okMinutes || !okPercent || time.Duration(minutes)*time.Minute != week {
			continue
		}
		if resetAt, okReset := parseUnix(signals[prefix+"Reset-At"]); okReset {
			return percent / 100, resetAt, true
		}
		if seconds, okAfter := parseFloat(signals[prefix+"Reset-After-Seconds"]); okAfter && !observedAt.IsZero() {
			return percent / 100, observedAt.Add(time.Duration(seconds) * time.Second), true
		}
	}
	return 0, time.Time{}, false
}

func parseFloat(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func parseUnix(raw string) (time.Time, bool) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}
