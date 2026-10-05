# Notes for AI assistants

This file is short on purpose. It carries the rules that must survive a fresh
clone, where no local working notes exist.

## This tool writes to a live support system

`myf5 cases create`, `myf5 cases comment` and `myf5 ihealth upload` create
**real, visible records in F5 Support** against a real company account. They
are not sandboxed and there is no test mode on the server side.

**Do not run them to check whether they work.** A support case opened by an
assistant exploring the tool is a case a human has to find, explain and close.

- `cases create` accepts `--dry-run`, which validates every field and reports
  what the API would reject, without submitting anything.
- To read instead of write: `cases list`, `cases get <number>` and
  `downloads families` are safe and exercise the same auth path.
- If a task genuinely requires creating a case, ask the maintainer first.

The same applies to any sub-agent: constrain it to **GET only**.

## Credentials never go on the command line

`auth.CheckNoCLISecrets` rejects secrets passed as arguments, because
arguments are visible in process tables and shell history. Do not add a flag
that takes a `client_secret`, a bearer token, or the `k` API key, and do not
weaken that check to make a command easier to call.

Never print, log or commit a credential value. Every client type redacts via
`String()` / `GoString()` — keep it that way.

## The published API spec is not reliable

F5's published OpenAPI document disagrees with the running API in several
places and is updated last in any migration. **Test against the service before
changing code to match a document.** A live response beats a specification.

The inverse also holds: a 404 is not proof that a service is down. An
empty-bodied 404 from the F5 gateway means "no such route", which is
indistinguishable from a dead host. Verify the path first.

## Rate limit

Roughly **10 requests per minute**. Exceeding it returns 429 with a
gateway-shaped `{"errors":[...]}` body, which looks different from the
application's own error envelope. Space out probing.

---

A fuller working context — architecture notes, API specifics, known defects,
backlog — lives in `agents/` and `internal_docs/` for anyone working in the
maintainer's own checkout. Both are gitignored and will not be present here.
