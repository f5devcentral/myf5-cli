# Branch Protection for `main`

> **Status: enabled.** Protection is active on `main`. It first took effect
> during the merge of PR #6 on 2026-10-05, which it blocked until the rule
> was adjusted. This records the configuration and the command to reproduce
> it.
>
> Required reviews were **removed** deliberately. This is a
> single-maintainer repository, and GitHub refuses to let an author approve
> their own pull request, so a required-approval rule could never be
> satisfied — every merge would need an administrator override, and a rule
> bypassed on every use protects nothing while making the merge log harder
> to read. The required status checks below are the half that does the work,
> and they remain enforced.

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
  "required_pull_request_reviews": null,
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
- **`required_pull_request_reviews: null`** is not an oversight. An earlier
  revision of this document proposed `required_approving_review_count: 1`
  and warned in the same breath that it "stops work entirely" on a
  single-maintainer repository. That warning was then ignored when the rule
  was applied, and PR #6 — 29 commits, all twelve checks green — deadlocked
  on a review that nobody existed to give. GitHub does not permit an author
  to approve their own pull request, and there is no flag that relaxes it.
  Restore this rule only when a second person with write access is actually
  reviewing; until then it buys nothing and costs an admin override on
  every merge.

## Verifying

```bash
gh api repos/SalesAmerSP/myf5-cli/branches/main/protection \
  --jq '{checks: .required_status_checks.contexts, strict: .required_status_checks.strict, reviews: .required_pull_request_reviews}'
```

`reviews` should be `null`. If it reports an approving-review count instead,
the rule has been reinstated and self-merge will deadlock — see the note
above before working around it.

A direct push to `main` should then be refused:

```
remote: error: GH006: Protected branch update failed for refs/heads/main.
```
