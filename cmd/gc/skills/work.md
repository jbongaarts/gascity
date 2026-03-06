# Work Items (Beads)

Everything in Gas City is a bead — tasks, messages, molecules, convoys.
The `bd` CLI is the primary interface for bead CRUD.

## Creating work

```
bd create "title"                      # Create a task bead
bd create "title" -t bug               # Create with type
bd create "title" --label priority=high # Create with labels
```

## Finding work

```
bd list                                # List all beads
bd ready                               # List beads available for claiming
bd ready --label role:worker           # Filter by label
bd show <id>                           # Show bead details
```

## Claiming and updating

```
bd update <id> --claim                 # Claim a bead (sets assignee + in_progress)
bd update <id> --status in_progress    # Update status
bd update <id> --label <key>=<value>   # Add/update labels
bd update <id> --note "progress..."    # Add a note
```

## Closing work

```
bd close <id>                          # Close a completed bead
bd close <id> --reason "done"          # Close with reason
```

## Hooks

```
gc hook show <agent>                   # Show what's on an agent's hook
gc agent claim <agent> <id>            # Put a bead on an agent's hook
```
