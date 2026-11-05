## What and why

<!-- What changes, and what problem it solves. Link the issue if there is one. -->

## How it was tested

<!-- Tests added (a failing test first for bug fixes). For UI changes: what you saw. -->

## Checklist

- [ ] `make check` passes (gofmt, vet, race tests, cross-platform vet)
- [ ] Tests cover the change; anything that writes into another tool's config proves that uninstall removes exactly it
- [ ] README / help text / `CHANGELOG.md` (Unreleased) updated where behavior changed
- [ ] No new dependency (or the PR explains why one is worth it)
