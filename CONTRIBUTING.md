# Contributing to gh-spruce

Thanks for helping! Good first contributions:

- **A new check** – add it to `internal/checks/rules.go`, register it in `All()`
  (`internal/checks/checks.go`) and cover it in `checks_test.go`.
- **A new CI template** – add `internal/templates/files/ci/<stack>.yml`, map the
  stack in `ciTemplate` (`internal/fix/plan.go`) and extend `templates_test.go`.
- **Better stack detection** – `internal/stack/stack.go` with a table test case.
- **A card theme** – add it to `Themes` in `internal/card/card.go`.

## Development

```bash
go test ./...                 # unit tests (no network, a fake GitHub API is used)
make lint                     # golangci-lint
make golden                   # refresh SVG golden files after changing the card
make install                  # install your local build as `gh spruce`
```

Rules of thumb:

1. Never print or download the contents of files that may hold secrets.
2. Every write must be previewable with `--dry-run` and go through a PR by default.
3. Keep API calls per repository low; prefer data already in the scan snapshot.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/).
