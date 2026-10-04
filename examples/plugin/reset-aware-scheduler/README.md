# Reset-Aware Scheduler Plugin

Routes each request to the account whose weekly limit resets soonest while it still has quota left, so quota that would expire unused at the reset gets spent first.

Each candidate is weighted by `remaining * (7 days / time to weekly reset)^exponent` and picked at random by weight. The inputs come from the host's passive quota snapshot of the last upstream response per account (`anthropic-ratelimit-unified-7d-*` for Claude, the 10080-minute `x-codex-*` window for Codex). Accounts with no snapshot, or whose reset has passed since the snapshot, count as full with a week to go. When no candidate has quota left, the plugin leaves the pick to the configured built-in strategy.

With the default exponent of 3, accounts resetting in 4, 4, and 5 days split traffic 40/40/20. Add an account resetting in 1 day with 35% left and it takes about 90%.

## Configuration

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    reset-aware-scheduler:
      enabled: true
      priority: 1
      exponent: 3
```

## Build

```bash
cd go
go build -buildmode=c-shared -o reset-aware-scheduler.so .   # .dylib on macOS
```
