# Comuse spike check evidence

Record observed results only. A source compile or mock check does not qualify
live Accessibility behavior, fixture launch, input permission, release, or
installed-app acceptance.

## Environment

The runner script writes the exact commit, OS/tool versions, selector, and
artifact locations to `environment.txt`. It requires a clean source worktree
and rejects exact-head evidence if the source or HEAD changes during checks.

## Checks

`status.tsv` records one of `pass`, `fail`, `held`, or `not_run` for every
selected check. `commands.log` contains command lines, output, and exit status.

## Fixture and permission state

Fixture compile checks do not launch the app. Accessibility and Input
Monitoring TCC state must be recorded as `not_run` unless separately performed
through the approved operator gate with an identified fixture build.

## Limits

State whether evidence is Go source tests, Swift tests, macOS 14 deployment
target compilation, real Go-to-Swift ABI smoke, mock adapter coverage, or live
operator acceptance. Never describe compile-target success as execution on
that target OS.
