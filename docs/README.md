# Zubi documentation

Start with [`AGENTS.md`](../AGENTS.md) at the repo root. It is the short version, and it links here for
anything that needs more than a line.

Each document below has one job and one trigger: the change that obliges you to edit it. A document that
contradicts the code is a defect, so fix it in the commit that made it wrong.

| Document | Covers | Edit it when |
| --- | --- | --- |
| [development.md](development.md) | The local loop: requirements, signing setup, XcodeGen, build, the dev channel | the local toolchain changes |
| [release.md](release.md) | How a build reaches a user: the push-to-`main` release and its five secrets | the pipeline changes |
| [signing.md](signing.md) | The local self-signed identity, the Developer ID release identity and its secrets, the hardened runtime and entitlements, what the updater trusts, quarantine | the signing setup changes |

## Features

One document per feature, covering its invariants and internals. Every one of them opens with an
`## Invariants` section; read it before changing anything in that area.

[updates](features/updates.md)

The HTTP API itself is documented in the [README](../README.md#api), next to the setup steps a user
follows to reach it.

## Contributing

[`CONTRIBUTING.md`](../CONTRIBUTING.md) covers the workflow: what to open, what to test, what a PR needs.
[`SECURITY.md`](../SECURITY.md) covers vulnerability reports.
