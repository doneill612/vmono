# Security policy

## Supported versions

Only the latest release is supported. Fixes ship in a new release.

## Reporting a vulnerability

Please don't open a public issue or pull request. Report it privately through [GitHub's vulnerability reporting](https://github.com/doneill612/vmono/security/advisories/new).

Include:

- the output of `vmono --version`, your OS and your git version
- the harness involved, if any
- steps to reproduce, and what an attacker gains

## Scope

Examples of what to report:

- vmono breaking one of the [guarantees it makes](docs/safety.md#what-vmono-guarantees)
- a command running that came from neither your config nor a package manifest you accepted
- generated harness permission rules that allow, without asking, something that changes your repos
- a way to tamper with the install scripts or release downloads

Some behavior is by design. Hooks and `setup` and `teardown` commands run as you, outside any sandbox, and vmono doesn't restrict what an agent may do: [your harness's permission system does](docs/safety.md#what-your-harness-enforces).
