# Audit and Debug Logging — Design Notes

> **Status: not implemented.** This records the requirement and the design
> constraints while they are fresh. Nothing described here exists in the tool
> yet.

## Why

Customers hitting a problem currently have nothing to send us but a one-line
error. Diagnosing the MyF5 API from the outside is hard in a specific way:
the server reports failures in at least four different shapes, and several of
them are actively misleading.

Real examples, all observed on 2026-10-05:

| What the user sees | What is actually wrong |
|---|---|
| `400 "Serial Number,Product Family and Product are required"` | Exactly one of them is missing. The message always names all three. |
| `403 "The value entered is invalid or out of Support"` | The serial number. Same status code the gateway uses for a bad API key, so it reads as a credentials failure. |
| `500 "Malformed JSON: Expected '{'"` | A `GET` with no body at all. Server-side defect. |
| `404` with an empty body | A path prefix was wrong. No hint which part. |

None of these can be told apart without seeing the request that produced them.
Working out the downloads `404` in this session took a side-by-side comparison
of two URLs — something the user could have supplied directly from a debug
log, had one existed.

## Two separate features

They are often conflated. They have different audiences and different rules.

### Audit log — always on

A durable record of **what the tool did**, for the operator's own
accountability. Writes only; reads are not interesting.

Should capture: timestamp, the action (`cases create`, `cases comment`,
`ihealth upload`, `bigip push`), the target (case number, host, filename),
the outcome, and the resulting identifier (new case number, qkview id).

Rationale: these commands change state in F5's systems and on customer
appliances. "Who ran what against which box, and when" is a question that
gets asked after an incident, and the answer should not depend on someone
having kept their terminal scrollback.

Default location `~/.myf5/audit.log`, mode `0600`, appended as JSON lines.
Never contains credentials or payload bodies — it records that a comment was
posted, not its text.

### Debug log — opt-in

Full request and response detail for **reporting a bug**, enabled per
invocation with `--debug` or `MYF5_DEBUG=1`, written to stderr or to a named
file.

Should capture: method, full URL, request headers, request body, status code,
response headers, response body, and elapsed time — for every HTTP call,
including redirects and the legacy downloads fallback.

## The hard constraint: redaction

A debug log is something a customer emails to us, or pastes into a GitHub
issue. It will leak whatever we put in it. The project already has a
zero-secret posture — secrets are rejected as CLI arguments outright, and
every client type implements `String()` to redact its own token — and the
logger must not be the hole in it.

Must be redacted everywhere, with no exceptions:

- `Authorization: Bearer ...` headers → `Bearer [REDACTED]`
- the `k` query parameter → `k=[REDACTED]` (it is a credential even though
  it is a published per-API-family constant)
- `client_secret` in OAuth token request bodies
- BIG-IP / BIG-IQ passwords in iControl REST bodies
- `Set-Cookie` and any session token in a response

The existing `redactedURL` pattern in `pkg/cases` and `pkg/downloads` is the
right model and should be lifted into one shared helper rather than
reimplemented per package — four copies of a redaction rule is four chances
to miss one.

Worth considering: a redaction unit test that feeds a known secret through
every log path and asserts it does not appear in the output. That is the kind
of guarantee that should be mechanical, not a matter of reviewer attention.

## Suggested shape

- `pkg/logging` with an `AuditLogger` and a `DebugLogger`.
- The debug logger wraps `http.RoundTripper`, so every client gets it by
  construction and no call site can forget to log.
- Audit entries written by the command handlers, where the semantic action is
  known. The transport layer cannot tell `cases comment` from `cases create` —
  both are just a POST to it.
- Size cap and rotation on both files. A qkview upload must not write a
  gigabyte of multipart body into a log.

## Open questions

- Should the audit log be opt-out? Always-on file writes may surprise people
  in restricted environments or CI.
- Does the debug log belong on stderr by default, or in a file that the tool
  then names for attaching to a report? The latter is friendlier for the
  "send this to support" workflow that motivates the feature.
- Is there a redaction mode safe enough to make `--debug` output pasteable
  without review, or should the tool always print a "check this before sharing"
  warning?
