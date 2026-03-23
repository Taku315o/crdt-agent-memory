# CI/CD

## 1. Overall Policy

- Pull requests are the only path to `dev` and `main`.
- `dev` is the integration branch.
- `main` is the release branch.
- Preview artifacts are built on pull requests and uploaded as short-lived workflow artifacts.
- Staging releases are created from tags like `v1.2.3-rc.1`.
- Production releases are created from tags like `v1.2.3`.

This split matches the project shape:

- The repository ships native binaries and embedded SQLite extensions.
- Release correctness depends on OS and architecture specific builds.
- The project does not yet have a long-running hosted control plane, so the real deployable unit is the signed binary release itself.

## 2. Branch Strategy

- `feature/*` -> PR into `dev`
- `dev` -> integration, CI required, preview artifacts available
- `release/*` or stabilizing PR -> PR from `dev` into `main`
- `main` -> protected branch, CI + CodeQL required
- tags on `main`
  - `vX.Y.Z-rc.N` -> staging prerelease
  - `vX.Y.Z` -> production release

## 3. Workflow Set

- `.github/workflows/ci.yml`
  - formatting, vet, tests, security checks, preview build artifacts
- `.github/workflows/release.yml`
  - cross-platform release build, checksums, SBOM, attestation, GitHub Release publish
- `.github/workflows/codeql.yml`
  - static analysis on `main` and weekly schedule

## 4. Environment Strategy

### Preview

- Trigger: pull request
- Artifact: short-lived zipped release bundles
- Goal: reviewer download and smoke-check without polluting releases

### Staging

- Trigger: `vX.Y.Z-rc.N`
- GitHub Environment: `staging`
- Controls:
  - required reviewers
  - optional environment-scoped secrets later
- Output: prerelease on GitHub Releases

### Production

- Trigger: `vX.Y.Z`
- GitHub Environment: `production`
- Controls:
  - required reviewers
  - release tag protection
- Output: immutable production release assets

## 5. Required Secrets and Variables

### Required custom secrets

- none

The workflows only require the built-in `GITHUB_TOKEN`.

### Recommended GitHub environment settings

- `staging`
  - required reviewers enabled
- `production`
  - required reviewers enabled
  - wait timer optional

## 6. Caching Strategy

- `actions/setup-go` module and build cache on CI and release jobs
- no custom cache for release archives
- preview artifacts retained for 7 days
- release build artifacts retained for 30 days before publish completion

## 7. Test Strategy

- PR / `dev` / `main`
  - `gofmt -l`
  - `go vet -tags sqlite_fts5 ./...`
  - `go test -tags sqlite_fts5 ./...`
- release
  - build all target binaries again from tagged commit

## 8. Security Strategy

- `govulncheck`
- `gosec`
- `gitleaks`
- `dependency-review-action` on pull requests
- `CodeQL` on `main` and weekly
- release artifact provenance attestation
- CycloneDX SBOM generation on release

## 9. Rollback and Failure Handling

- CI failure blocks merge by branch protection
- failed staging tag is fixed by cutting a new `-rc` tag; never mutate assets in place
- failed production release is rolled back operationally by:
  - marking the bad release as deprecated
  - re-promoting the previous stable tag
  - cutting a fixed patch tag `vX.Y.(Z+1)`
- release workflow uses one publish step after all platform builds succeed, so partial publication is avoided

## 10. Operational Rules

- protect `dev` and `main`
- require status checks:
  - `test`
  - `security`
  - `build-preview`
  - `actionlint` when workflows changed
  - `codeql / analyze` on `main`
- disable force push on `main`
- restrict tag creation for `v*`

## 11. Dangerous Patterns To Avoid

- building production assets from branch heads instead of tags
- mixing preview and production artifacts in one release workflow
- allowing direct pushes to `main`
- using mutable release assets as rollback
- skipping cross-platform builds even though embedded native extensions are platform-specific

## 12. Future Extensions

- add package publishing to Homebrew, Scoop, and deb/rpm repositories
- add signed container images if a control-plane service is introduced
- add deployment workflows once a real staging or production runtime exists
