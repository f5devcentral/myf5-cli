# Open Questions About the MyF5 APIs

Seven behaviours this project has observed but cannot explain from outside
F5. Each is currently blocking work, recorded as an assumption, or logged as
an unexplained server-side failure.

The fenced block below is written to be pasted into an assistant with access
to internal announcements, engineering wikis and ticketing — but the
questions stand on their own as the list of what we do not know.

> Answers belong in `openapi-observed.json`, annotated with their source.
> This repository keeps observed behaviour, documented behaviour and
> confirmed intent as three distinct categories, and conflating them is how
> the published specification came to be trusted longer than it deserved.

---

```
I maintain an external CLI that calls the public MyF5 APIs
(support.apis.f5.com, api.software.downloads.f5.com, ihealth2-api.f5.com)
using OAuth2 client credentials. Several things changed recently and at least
one endpoint is returning a server error. Please search internal
announcements, engineering wikis, release notes, change records, incident
tickets and Salesforce/API team documentation, and answer the questions below.

For each answer, cite the internal source (document title, ticket ID, channel
and date). If you cannot find anything, say so explicitly rather than
inferring — I would rather have a confirmed gap than a plausible guess.

== 1. Case metadata endpoint returning 500 (highest priority) ==

GET https://support.apis.f5.com/case-management/v1/cases/metadata?k=<key>

returns, for every request:

  HTTP 500
  {"status":"Error","message":"Malformed JSON: Expected '{' at the beginning
   of object","isError":true,"data":null,"code":500}

Confirmed over roughly six hours on 2026-10-05, with and without a request
body, with three different Accept headers, after a full rate-limit cooldown.
Other endpoints on the same host, with the same token and same `k` key,
return 200 normally — so this is specific to /metadata, not to my
credentials or client.

Note the error mentions malformed JSON on a GET that sends no body at all.

  - Is there an open incident or known issue for this endpoint?
  - Did a deployment around 2026-10-02 to 2026-10-05 change it?
  - Is there an ETA, or a documented alternative source for the valid
    productFamily / product / version / priority / reasonForContact /
    preferredContactMethod / timezone values that case creation requires?

== 2. Entitlement requirement on case creation ==

POST https://support.apis.f5.com/case-management/v1/cases rejects a create
that omits an entitlement identifier:

  BIG-IP without serialNumber    -> 400 "Serial Number,Product Family and
                                         Product are required to create Case."
  NGINX  without subscriptionId  -> 403 "Unable to verify Subscription.
                                         Please make sure Subscription Number
                                         and Product Family vaules are
                                         provided."   [sic - "vaules"]

But the MyF5 web UI creates cases WITHOUT one: case 01292481 was opened on
2026-10-05 as a BIG-IP case with its serial/registration-key field empty. The
UI does not call this API — it writes to Salesforce directly via
CaseManagementCreateCaseCtllerWOSharing.upsertCase.

  - Is the API's entitlement check intentional policy, or an unintended
    divergence from the UI's validation?
  - Is there a documented way for an authenticated API client to open a case
    without a serial or subscription — a flag, a different endpoint, a scope
    or entitlement on the API credential?
  - F5 Systems Engineers can open presales cases in the UI. Is there an
    equivalent path through the API?

== 3. Published OpenAPI specification accuracy ==

The published spec (my.f5.com/manage/myf5ApiReference) disagrees with the
running API in at least these ways:

  a. serialNumber is marked optional; the API requires it (see 2).
  b. The CaseCreateApiRequest schema's required list names `version`, while
     every example in the same document sends `productVersion`.
  c. /support/case-management/v1/... appears in documentation but returns
     404 "Unable to identify proxy for host".
  d. The rateLimitParam_Cases example key
     (X4Y6fGWJdC1qLhTsCSDW4lPcqKvF5g5r) is rejected by the live Case API
     with 400 "Missing or invalid k query parameter".
  e. Several endpoints (customer-catalog, knowledge-management) carry server
     URLs pointing at support-stg.apis.f5networks.net, a staging host, in
     what is published as production documentation.

  - Who owns this specification, and is there an internal source of truth
    that is more current?
  - Is there a changelog for the API that is not in the public document?

== 4. Downloads API path shape ==

The Downloads endpoints are served at

  https://api.software.downloads.f5.com/v1/downloads-management/v1/downloads/...

with a DOUBLED v1 — the same path without the leading /v1 returns 404. An
announcement told integrators to move to this host and drop the `k`
parameter, which matches observed behaviour.

  - Is the doubled /v1 intentional and stable, or an artifact of the ESDM to
    ESDP migration that is expected to change?
  - Is the legacy support.apis.f5.com downloads path being retired, and on
    what date? I currently keep a fallback to it and would like to remove
    that if it is dead.

== 5. The `k` API usage key ==

Case endpoints require a `k` query parameter; Downloads and iHealth do not.
My understanding is that these are per-API-family constants published in the
specification rather than per-user secrets.

  - Is that correct, and are they rotated? On what schedule, and is rotation
    announced anywhere I can subscribe to?
  - Is there a per-customer key I should be using instead?

== 6. Rate limits ==

I observe roughly 10 requests per minute per client, after which the gateway
returns 429 with a body shaped unlike every other error on the service:
{"errors":[{"id":"rrt-...","code":"rate-limit","message":"Request rate
exceeded 10pm"}]}.

  - Is 10/minute the documented limit, is it per-client or per-account, and
    can it be raised for a sanctioned internal tool?
  - Are the limits different per API family?

== 7. Case detail dateOpened field ==

GET /case-management/v1/cases/{caseNumber} returns the PRIORITY string in
the dateOpened field (observed: "4 - General Assistance"), while
GET /case-management/v1/cases returns a correct timestamp
("09/29/2026 17:59:42") for the same case.

  - Is this a known field-mapping defect? Should I file a ticket, and where?

== What I am building, for context ==

An unofficial Go CLI used by F5 Systems Engineers to list, read, comment on
and create support cases, upload QKViews to iHealth, and download software.
It is not an official F5 product. I would particularly like to know of any
internal guidance on sanctioned use of these APIs by internal tooling, and
whether an internal API team should be reviewing it.
```

---

## What to do with the answers

Anything confirmed from an internal source should land in
`docs/openapi-observed.json` as an `x-observed-divergence` entry or an
`x-verified` annotation naming that source. Keep the distinction this project
already maintains: observed behaviour, documented behaviour, and now
internally-confirmed intent are three different things, and the file says
which is which.
