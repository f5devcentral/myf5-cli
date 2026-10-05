# Valid Case Field Values

The API's own source of these values, `GET /case-management/v1/cases/metadata`,
has been returning `500 "Malformed JSON"` for every request since at least
2026-10-05. While that outage lasts, `myf5 cases create` cannot tell you what
to pass, and neither can the specification — F5's published document describes
these fields as "returned via Case Metadata API" and lists no values.

The values below were recovered from a HAR capture of the MyF5 web UI opening
case `01292481` on 2026-10-05. They are what the web form itself offered, so
they are authoritative for the UI's own taxonomy. They are **not** confirmed
against the public API: it rejects creates for an unrelated reason (see
[cli.md](./cli.md#create-flags)), so no create has yet succeeded through it.

> Treat this file as a stopgap. When the metadata endpoint recovers, prefer
> `myf5 cases metadata` and delete this.

## Priority

Four values. These match F5 article K2633 exactly, which is a useful
cross-check on the extraction.

```
1 - Site Down
2 - Site At Risk
3 - Performance Degraded
4 - General Assistance
```

MyF5 sometimes also displays a "Low" option. K2633 states it is shown in error
and is not a valid case priority.

## Reason for contact

```
Account Related
General Problem
Informational
Other
Pre Sales
Proactive
Security Related
Subscription / Licensing
```

`Pre Sales` is available to F5 Systems Engineers; a customer account cannot
open a presales case.

## Preferred contact method

```
Email
Mobile
Phone
```

## Product family

58 values were present. The ones most likely to be wanted:

```
BIG-IP
NGINX
Aspen Mesh
F5OS
BIG-IQ
BIG-IP Next for Kubernetes
```

Selected BIG-IP products, to show the shape of the `product` field — it is the
specific module, not the family:

```
BIG-IP LTM
BIG-IP DNS
BIG-IP APM
BIG-IP ASM
BIG-IP AFM
BIG-IP AVR
BIG-IP PEM
BIG-IP Advanced WAF
BIG-IP Datasafe
```

The full list also covers AI Security, AI Guardrails, AI Red Team, API
Security Local Edition, the BIG-IP Next CNF range and SPK. Re-extract from a
HAR if you need one that is not here.

## Timezone

70 values, all of the form `(GMT ±HH:MM) <NAME>`. The format is finicky and
the API matches on the whole string, so copy one verbatim rather than
constructing it. Examples:

```
(GMT -5:00) US/Eastern - Eastern Standard Time
(GMT -6:00) US/Central - Central Standard Time
(GMT +10:00) GMT+10:00
(GMT +10:00) AET - Australian Eastern Daylight Time (New South Wales)
(GMT +12:45) NZ-CHAT - Chatham Daylight Time
```

Note the two distinct styles in that list: bare offsets such as
`(GMT +10:00) GMT+10:00`, and named zones such as
`(GMT -5:00) US/Eastern - Eastern Standard Time`. Both occur. F5's own
specification examples use the bare-offset style; the value observed on a real
case used the named style.

## Fields the web UI sends

For reference, the web form submits these to Salesforce. The names are
Salesforce field names, not the public API's — the two differ, which is worth
remembering when reading either.

| Salesforce field | Public API field |
|---|---|
| `Product_Group__c` | `productFamily` |
| `Product__c` | `product` |
| `Version__c` | `version` |
| `Priority` | `priority` |
| `Reason_for_Contact__c` | `reasonForContact` |
| `Preferred__c` | `preferredContactMethod` |
| `Time_zone__c` | `timezone` |
| `Subject` | `subject` |
| `Description` | `description` |
| `Hostname__c` | `hostName` |
| `Cloud_Provider__c` | `cloudProvider` |
| `Customer_ticket_number__c` | `customerTicketNumber` |
| `Reg_Key_Serial_Number_Formula__c` | `serialNumber` |
| `Origin` | — (the UI sends `Web`; the API has no equivalent) |
| `AccountId`, `ContactId` | — (derived from the authenticated identity) |

The serial field was **empty** on case 01292481, a BIG-IP presales case. That
is the evidence that the public API's insistence on an entitlement identifier
is a constraint of that API rather than of support cases generally.
