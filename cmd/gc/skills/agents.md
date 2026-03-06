# Agent Management

Agents are the workers in a Gas City workspace. Each runs in its own
session (tmux pane, container, etc).

## Listing and inspecting

```
gc agent list                          # List all agents and their status
gc agent peek <name>                   # Capture recent output from agent session
gc agent peek <name> 100               # Peek with custom line count
gc agent status <name>                 # Show detailed agent status
```

## Adding agents

```
gc agent add --name <name>             # Add agent to city root
gc agent add --name <name> --dir <rig> # Add agent scoped to a rig
```

## Communication

```
gc agent nudge <name> <message>        # Send a message to wake/redirect agent
gc agent attach <name>                 # Attach to agent's live session
gc agent claim <name> <bead-id>        # Put a bead on agent's hook
```

## Lifecycle

```
gc agent suspend <name>                # Suspend agent (reconciler skips it)
gc agent resume <name>                 # Resume a suspended agent
gc agent drain <name>                  # Signal agent to wind down gracefully
gc agent undrain <name>                # Cancel drain
gc agent drain-check <name>            # Check if agent has been drained
gc agent drain-ack <name>              # Acknowledge drain (agent confirms exit)
gc agent request-restart <name>        # Request graceful restart
gc agent kill <name>                   # Force-kill agent session
```
