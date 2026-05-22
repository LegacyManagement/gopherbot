# Engine-Owned Plugin Commands

The engine owns every plugin command token beginning with `_`. Plugin
configuration must reject any `Commands` or `MessageMatchers` entry with a
leading underscore so that engine lifecycle callbacks cannot collide with
administrator-defined command names.

Current engine-owned plugin commands:

- `_configure`: ask an external plugin for default YAML config.
- `_init`: initialize a plugin after post-connect configuration load.
- `_authorize`: ask the configured authorizer to approve a command.
- `_usergroups`: ask the authorizer for help/filtering group membership.
- `_elevate`: ask the configured elevator to confirm elevated execution.
- `_catchall`: invoke a catch-all plugin after addressed command routing misses.
- `_subscribed`: deliver an unmatched message in a subscribed thread.
- `_expiresub`: notify a subscribed plugin that its thread subscription expired.

Jobs remain separate. The scheduler/dispatcher uses an internal `run` command
when starting jobs, but job handlers receive only their configured/runtime
arguments, not the command token.

When adding an engine callback:

1. Use a leading `_` command token.
2. Do not expose the token through plugin `Commands` or `MessageMatchers`.
3. Add load-time validation if the callback creates a new configurable command
   surface.
4. Update `aidocs/EXTENSION_API.md`, `aidocs/INTERPRETERS.md`, and
   `aidocs/PIPELINE_LIFECYCLE.md` if plugin authors need to handle it.
