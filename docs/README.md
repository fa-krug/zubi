# Zubi documentation

New contributors start with [`CONTRIBUTING.md`](../CONTRIBUTING.md); [`AGENTS.md`](../AGENTS.md) at
the repo root is the short version. Both link here for anything that needs more than a line.

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

[updates](features/updates.md) · [relay](features/relay.md)

The user-facing documentation, including the HTTP API, lives on the website,
<https://zubi.fa-krug.de/>, built from [`site/`](../site); its API reference is generated from
the contract. The [README](../README.md) is the end-user front door. The contract is
[`Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json`](../Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json),
which the package's tests hold the server to; see [development.md](development.md#the-api-package).

## Contributing

[`CONTRIBUTING.md`](../CONTRIBUTING.md) is the developer front door: the repository layout, setup, building
and testing each part, changing the API, and what a PR needs.
[`SECURITY.md`](../SECURITY.md) covers vulnerability reports.
