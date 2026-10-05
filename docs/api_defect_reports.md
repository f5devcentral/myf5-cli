# API Defect Reports

Two reproducible server-side defects in the MyF5 Support Case API, written to
be filed as internal tickets. Both were isolated with a control call proving
the client, credentials and network path are sound.

A third item is included as a documentation defect rather than a service one.

---

## 1. `GET /case-management/v1/cases/metadata` returns 500 for every request

**Severity:** High — this endpoint is the documented source of the valid field
values required to create a case, so case creation through the API cannot be
performed correctly while it is down.

**Endpoint**

```
GET https://support.apis.f5.com/case-management/v1/cases/metadata?k=<key>
Authorization: Bearer <token>
Accept: application/json
```

**Observed**

```
HTTP 500
{"status":"Error","message":"Malformed JSON: Expected '{' at the beginning of object",
 "isError":true,"data":null,"code":500}
```

**Expected:** 200 with the case-creation metadata.

**Note the contradiction:** the error reports malformed JSON on a `GET` that
carries no request body. There is no client-supplied JSON to be malformed.

### Why this is server-side

Each row below is a separate live request. The control is the decisive one: it
is the same client, token, `k` key, host and code path, differing only in the
path segment.

| Request | Result |
|---|---|
| **Control** — `GET /cases?type=ALL_OPEN_CASES` | **200**, real case data |
| `GET /cases/metadata` as sent by the client | **500** Malformed JSON |
| `GET /cases/metadata`, `Accept: */*` | **500**, byte-identical |
| `GET /cases/metadata`, no `Accept` header | **500**, byte-identical |
| `GET /cases/metadata`, body `{}` | **500**, byte-identical |
| `GET /cases/metadata/` (trailing slash) | 404 `not-found` (gateway shape) |
| `GET /cases/metadata` with no `k` | 400 `bad-request` (gateway shape) |

The last two rows matter: a missing key or a wrong path produces a *gateway*
error with an `{"errors":[...]}` body. The 500 arrives in the application's
own `{"status","message","isError","code"}` envelope, so the request is
reaching the service behind the gateway and failing there.

Three `Accept` variations produce byte-identical responses, so the failure
does not depend on anything the client controls.

**Duration:** continuously from first observation on 2026-10-05, retested
after a full rate-limit cooldown roughly six hours later. Not transient, and
not a rate-limit artefact.

**Suggested investigation:** something server-side appears to be parsing a
document that is absent or malformed — a backing configuration, or a
downstream response being deserialised — rather than anything in the request.

---

## 2. `dateOpened` returns the priority string on case detail

**Severity:** Low — wrong data in a response field, silently. A client that
trusts it will display or store a priority where a date belongs.

**Endpoint**

```
GET https://support.apis.f5.com/case-management/v1/cases/{caseNumber}?k=<key>
```

**Observed:** for a case whose list entry reports
`"dateOpened": "09/29/2026 17:59:42"`, the detail endpoint returns
`"dateOpened": "4 - General Assistance"` — the value of the `priority` field.

The list endpoint is correct for the same case; only the detail response is
affected. Both were observed in the same session, minutes apart.

**Expected:** the same timestamp the list endpoint returns.

**Impact:** a consumer cannot tell a wrong value from a right one without
cross-checking against the list endpoint, which defeats the purpose of the
detail endpoint carrying the field at all. Our client suppresses the field on
detail responses as a workaround.

---

## 3. Published OpenAPI document does not match the live API

**Severity:** Medium — this is the document integrators are directed to, and
each inaccuracy below cost real debugging time.

Source: `https://my.f5.com/manage/myf5ApiReference`

| # | Document says | Live API does |
|---|---|---|
| 1 | `serialNumber` is optional, "applicable for only BIG-IP products" | Create is rejected without an entitlement identifier. BIG-IP requires `serialNumber`; NGINX and Aspen Mesh require `subscriptionId` |
| 2 | `CaseCreateApiRequest.required` names `version` | Every example in the same document sends `productVersion`. `version` is what the API accepts — the document contradicts itself |
| 3 | `/support/case-management/v1/...` is documented | 404 `"Unable to identify proxy for host"`. Only `/case-management/v1/...` exists |
| 4 | `rateLimitParam_Cases` example key `X4Y6fGWJdC1qLhTsCSDW4lPcqKvF5g5r` | Rejected with 400 `"Missing or invalid k query parameter"` |
| 5 | `customer-catalog` and `knowledge-management` endpoints | Carry server URLs pointing at `support-stg.apis.f5networks.net`, a **staging** host, in published production documentation |

### A further inconsistency worth a decision rather than a fix

The MyF5 web UI creates cases that this API would refuse. Case `01292481` was
opened through the UI on 2026-10-05 as a BIG-IP case with its
serial/registration-key field empty; the API rejects that same payload with
400.

The UI does not use this API — it writes to Salesforce directly through
`CaseManagementCreateCaseCtllerWOSharing.upsertCase` — so the two paths apply
different validation to the same operation.

Whether the API's stricter check is intended policy or an unintended
divergence is a question for the owning team. If it is intended, the published
document should say so, since it currently implies the opposite.

---

## Reporting notes

- Rate limit observed at ~10 requests/minute per client; the gateway returns
  429 with `{"errors":[{"code":"rate-limit","message":"Request rate exceeded
  10pm"}]}`. Worth stating in a ticket so the investigator does not mistake
  throttling for the defect.
- All observations are from an external Go client using OAuth2 client
  credentials against the public endpoints. No internal access was used, so
  everything here should be reproducible by anyone with API credentials.
