# City Lifecycle

A city is a directory containing `city.toml` and `.gc/` runtime state.

## Initialization

```
gc init                                # Initialize city in current directory
gc init <path>                         # Initialize city at path
```

## Starting and stopping

```
gc start                               # Start city (auto-inits if needed)
gc start <path>                        # Start city at path
gc start --foreground                  # Run as persistent controller
gc start --dry-run                     # Preview what would start
gc stop                                # Stop all agent sessions
gc restart                             # Stop then start
```

`gc start` starts all singleton agents and resumes existing multi-instance
agents, but does NOT create new multi instances. Use `gc agent start
<template>` to create new instances from a `multi = true` template.

## Status

```
gc status                              # City-wide overview
gc agent status <name>                 # Individual agent status
gc rig status <name>                   # Rig status
```

## Suspending

```
gc suspend                             # Suspend entire city
gc resume                              # Resume suspended city
```

## Configuration

```
gc config show                         # Show resolved configuration
gc config explain                      # Show config layering and provenance
gc doctor                              # Run health checks
```

## Events

```
gc events                              # Tail the event log
gc event emit <type> [data]            # Emit a custom event
```

## Packs

```
gc pack list                           # List installed packs
gc pack fetch                          # Fetch remote packs
```
