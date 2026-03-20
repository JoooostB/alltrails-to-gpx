# Contributing

Contributions are welcome! This is a personal homelab project, so the bar for
what gets merged is quality and fit — not volume.

## Before you start

Open an issue first for anything beyond a small bug fix. This avoids you
investing time in a change that turns out to be out of scope or that has
already been considered.

## Pull requests

- **Keep PRs focused.** One logical change per PR. A PR that fixes a bug and
  adds a feature will be asked to split.
- **Include tests.** New behaviour should come with tests. Bug fixes should
  include a test that would have caught the bug.
- **Follow existing code style.** Run `go fmt ./...` and `go vet ./...` before
  pushing. The CI pipeline enforces both.
- **Write a clear description.** Explain what the change does and why. If it
  fixes an issue, reference it (`Fixes #123`).

## Review

All pull requests are **reviewed by a human** before merging. Automated checks
(CI, Renovate dependency updates) are a prerequisite, not a substitute.

Expect feedback. The goal of review is to keep the codebase simple, correct,
and consistent — not to gatekeep. If a reviewer asks for changes, it's a
conversation, not a rejection.

## What's in scope

- Bug fixes
- Improvements to reliability or correctness of the AllTrails data fetching
- Security improvements
- Dependency updates (prefer opening an issue or letting Renovate handle these)

## What's out of scope (for now)

- AllTrails authentication / private routes
- Direct Strava integration (tracked as a future feature; reach out before
  starting work on this)
- Alternative conversion backends
- UI redesigns without a prior discussion

## Code of conduct

Be kind. This project follows the
[Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
