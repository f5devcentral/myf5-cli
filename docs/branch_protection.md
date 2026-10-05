# Branch Protection for `main`

> **Status: not enabled.** This records the intended configuration and the
> exact command to apply it. Enabling protection is a repository governance
> change, so it is left for a repo admin to run deliberately.

## Why

Everything merged into `main` should have passed CI on a branch first. The
project already works this way by convention — recent work has gone through
pull requests — but nothing enforces it, so a direct push to `main` would
succeed today.

Two specific risks this closes:

- **A direct push bypasses the secret scanner.** `Secret & Credential Leak
  Scan (Gitleaks)` only runs on pull requests and pushes to tracked branches.
  Credentials pushed straight to `main` would not be caught before landing.
- **A direct push bypasses the cross-platform test matrix.** The tool is built
  for macOS, Linux and Windows; a change that compiles locally can still fail
  on another platform, and `main` should never be in that state.

## Required status checks

These are the exact check names as GitHub reports them. They must match
character for character, including parentheses, or the rule silently never
fires.

```
Lint, Vet & Code Standards
Secret & Credential Leak Scan (Gitleaks)
Go Vulnerability Scanner (govulncheck)
Test on ubuntu-latest (Go stable)
Test on ubuntu-latest (Go 1.26.x)
Test on macos-latest (Go stable)
Test on macos-latest (Go 1.26.x)
Test on windows-latest (Go stable)
Test on windows-latest (Go 1.26.x)
```

Note that `Build Verification on <os>` is deliberately absent: it is skipped
when the test job fails, and a required check that reports "skipping" blocks
a pull request indefinitely.

## Applying it

Requires admin on the repository.

```bash
gh api -X PUT repos/SalesAmerSP/myf5-cli/branches/main/protection \
  --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "Lint, Vet & Code Standards",
      "Secret & Credential Leak Scan (Gitleaks)",
      "Go Vulnerability Scanner (govulncheck)",
      "Test on ubuntu-latest (Go stable)",
      "Test on ubuntu-latest (Go 1.26.x)",
      "Test on macos-latest (Go stable)",
      "Test on macos-latest (Go 1.26.x)",
      "Test on windows-latest (Go stable)",
      "Test on windows-latest (Go 1.26.x)"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "required_approving_review_count": 1,
    "dismiss_stale_reviews": true
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON
```

### Choices worth a second look

- **`strict: true`** requires a branch to be up to date with `main` before
  merging. It is the safer setting and the more annoying one: every merge to
  `main` obliges other open branches to rebase. With few concurrent
  contributors that is cheap; if the project gets busier, reconsider.
- **`enforce_admins: false`** lets an admin bypass protection in an emergency.
  Set it to `true` for a stricter posture, accepting that a broken `main`
  then needs a deliberate unprotect step to fix.
- **`required_approving_review_count: 1`** blocks self-merge. On a
  single-maintainer repository this stops work entirely, so set it to `0` if
  that is the situation — the status checks still apply and are the part that
  matters most here.

## Verifying

```bash
gh api repos/SalesAmerSP/myf5-cli/branches/main/protection \
  --jq '{checks: .required_status_checks.contexts, strict: .required_status_checks.strict, reviews: .required_pull_request_reviews.required_approving_review_count}'
```

A direct push to `main` should then be refused:

```
remote: error: GH006: Protected branch update failed for refs/heads/main.
```
