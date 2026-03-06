# Dispatching Work

`gc sling` routes work to agents. Three modes:

## Direct dispatch (bead to agent)

```
gc sling <agent> <bead-id>             # Route a bead to an agent's hook
```

The agent receives the bead on its hook and runs it per GUPP.

## Formula dispatch (formula on agent)

```
gc sling <agent> -f <formula>          # Run a formula, creating a molecule
```

Creates a molecule from the formula and hooks the root bead to the agent.

## Wisp dispatch (formula + existing bead)

```
gc sling <agent> <bead-id> --on <formula>  # Attach formula wisp to bead
```

Creates a molecule wisp on the bead and routes to the agent.

## Formulas

```
gc formula list                        # List available formulas
gc formula show <name>                 # Show formula definition
```

## Convoys (grouped work)

```
gc convoy create <name> <bead-ids...>  # Group beads into a convoy
gc convoy list                         # List active convoys
gc convoy status <id>                  # Show convoy progress
gc convoy add <id> <bead-ids...>       # Add beads to convoy
gc convoy close <id>                   # Close convoy
gc convoy check <id>                   # Check if all beads done
gc convoy stranded                     # Find convoys with no progress
gc convoy autoclose                    # Close convoys where all beads done
```

## Automations

```
gc automation list                     # List automation rules
gc automation show <name>              # Show automation definition
gc automation run <name>               # Manually trigger an automation
gc automation check <name>             # Check if gate conditions are met
gc automation history <name>           # Show automation run history
```
