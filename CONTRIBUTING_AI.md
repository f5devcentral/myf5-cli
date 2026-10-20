# AI Assistant Contribution Guidelines

This document defines mandatory safety and security invariants for AI coding assistants working in this repository.

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

## API Endpoint Verification

When modifying or extending API client paths, always verify request routes and response schemas against the live API endpoints (`myf5 doctor`, `myf5 cases list`, `myf5 downloads families`). Note that an empty-bodied HTTP `404` from the API gateway typically indicates an unmatched route path (for example, omitting the leading `/v1` gateway prefix) rather than a service outage.

## Rate Limiting

MyF5 Cloud API endpoints enforce a rate limit of approximately **10 requests per minute**. Exceeding the threshold returns HTTP `429 Too Many Requests`. Space out automated diagnostic queries and rely on the built-in client backoff logic.

