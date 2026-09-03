# Commercial licensing

whodar is source-available under the [Business Source License 1.1](LICENSE). For most
companies that is all you need, and it costs nothing.

## You do not need a commercial license to

- Run whodar in production inside your company, on your own infrastructure.
- Index your own Slack, GitHub, Jira, Confluence, and PagerDuty.
- Read, modify, fork, and self-host the source.
- Use it on any number of internal teams or seats.

The Additional Use Grant in the license permits internal production use outright. If
your legal team asks whether internal deployment triggers a source disclosure
obligation, the answer is no. BUSL restricts two things only: offering whodar to third
parties as a hosted service, and selling whodar-produced findings to third parties.

Each released version converts to Apache License 2.0 on the earlier of 2030-08-03
or the fourth anniversary of its first public distribution, and every restriction
on that version lapses then.

## You do need a commercial license to

- Offer whodar to third parties as a hosted or managed service that provides its
  primary functionality. That covers embedding whodar inside a hosted product
  you sell just as much as hosting whodar directly.
- Provide reports, assessments, or analyses produced with whodar to third
  parties as part of any paid service or engagement. Running it on your own
  organization is free; charging others for its findings is licensed work. A
  Partner License is what lifts this, and it exists precisely so diligence
  practices and fractional CTO firms can do it.
- Redistribute whodar, modified or not, under different license terms. BUSL
  redistribution is permitted, but every copy and derivative stays under BUSL
  with the license displayed.

Shipping whodar inside an on-premises product you sell is permitted by the BUSL
text as long as the license travels with it and nothing is offered as a hosted
service. If you want to do that under your own terms, without BUSL attached, or
with the whodar name on it, that is what a commercial license is for.

## If your policy blocks source-available licenses outright

Some organizations refuse anything that is not OSI-approved, regardless of the terms.
A commercial license resolves that: it replaces BUSL with a standard commercial
agreement, so the review question becomes a purchasing question.

## Getting one

Email hello@whodar.dev with the subject "Commercial license" and describe your use
case and rough scale. Terms depend on deployment size and whether you need support
commitments.

## What is not gated

Finding people and finding your past work need no license key, no seat count, no
telemetry, and no phone-home: the whole graph, every source, every surface, and
recall pointing back at the conversations you took part in. whodar runs local by
default and stays that way.

## Knowledge Continuity Assessment

The paid work is an assessment of one company: which parts of it rest on one
person, what leaves when they do, and where the declared owner is not the one
doing the work. It is built for the people who ask that question on a deadline
and pay for the answer, which is technical due diligence before an acquisition.

It runs on what a data room already holds: git history, a Slack export, an org
chart, a CODEOWNERS file. No credentials, no bot to install, no access to the
target's live systems, nothing sent anywhere. One command produces the whole
deliverable.

You get a report readable without whodar, the findings as data, the departure
impact for the most load-bearing people, and a LoomSeal bundle that anyone can
verify offline. The seal is the point: a board or an acquirer can confirm the
findings came from a licensed install and have not been edited since.

**From $7,500 per company assessed**, priced by how much code and how many
sources there are rather than by headcount. Five business days.

Running whodar on your own organization is free at any size, so there is no
enterprise tier: both paid things here are about somebody else's company,
having us assess one or licensing you to assess your clients'.

## Partner License

For diligence practices, fractional CTO firms, and anyone who runs assessments
for their own clients. The license removes the restriction in the source
license that otherwise forbids providing whodar findings to third parties as
part of a paid engagement, and names your firm inside every sealed report, so a
finding traces back to the practice that issued it.

**From $10,000 a year, flat.** Unlimited engagements, unlimited targets. There
is no seat count to true up and no audit, because whodar cannot phone home and
does not know your headcount.

## Memory

Memory keeps the words of Slack conversations on your own machines, so whodar
can still show how something was fixed after the messages themselves are gone.
Without it, whodar keeps a pointer and nothing more. Most companies delete
their own chat on a schedule for good legal reasons, and that purge also
deletes the engineering record.

It is available with an assessment or a partner license rather than sold on its
own. If you want it by itself, say so and we will price it.

## How licensing works

The license is a small signed file, verified against a key compiled into the
binary. Nothing is checked over the network, so a licensed install works
air-gapped. If a license expires, whodar drops to the free tier and every byte
already on disk stays exactly where it is: nothing is deleted, hidden, or held
hostage, and what you kept stays readable.

Ask at hello@whodar.dev.
