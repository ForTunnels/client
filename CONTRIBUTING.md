# Contributing

Thanks for considering a contribution to ForTunnels Client.

## Getting Started

- Fork the repository and create a feature branch.
- Keep changes focused and small when possible.
- Run checks before submitting:
  - `make check`

## Quality tools

The repository owns the versions of Go quality tools. Bootstrap them with
`./scripts/ci/ensure-quality-tools.sh`; it installs pinned binaries only under
`.cache/quality-tools/bin`, and the Make targets run it automatically when
needed. Do not use global `go install` copies or `@latest` for repository
checks. GolangCI-Lint v1.64.8 remains the enforcing lint lane; v2.12.2 is only
used by the separate migration-report command.

## Pull Requests

- Provide a clear description of the change and motivation.
- Include tests when behavior changes.
- Avoid unrelated refactors in the same PR.

## Security Issues

Do not open public issues for security vulnerabilities.
See `SECURITY.md` for the reporting process.
