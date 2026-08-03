# Security policy

Sextant manages fleets of laptops for public-sector organisations. A flaw here
can reach every device an operator is responsible for, so we would much rather
hear about a suspicion that turns out to be nothing than miss a real one.

## Reporting

Mail **security@bb-open.com** with what you found and how to reproduce it.

You do not need a proof of concept, an exploit, or certainty. "This looks wrong
and here is why" is a useful report.

What we do:

- **Acknowledge within three working days.** If you have not heard back by
  then, assume the mail went astray and chase it — do not assume you are being
  ignored.
- **Tell you what we think within ten working days**: whether we can reproduce
  it, how serious we judge it, and roughly when a fix lands.
- **Credit you** in the release notes, unless you would rather we did not.

Please give us a reasonable window before publishing. We will not use that
window to argue about severity — if we disagree with your assessment we will
say so plainly and you remain free to publish.

## What is in scope

The code in this repository: the console, the device agent, the validation
gate, the imaging station and the Helm chart.

Deployments are not. `console.bb-open.com` is BB Open's own instance, not a
test target; please do not probe it. If you want something to attack, run your
own — `docker-compose.yml` gets you a local one.

## What we consider serious

Roughly in order:

- Anything that lets one organisation's data or configuration reach another.
- Anything that puts configuration onto a device without passing the gate, or
  that lets a device be configured by someone who should not be able to.
- Secret material leaving where it belongs: LUKS recovery keys, device
  credentials, the age identity that opens a fleet's secrets.
- Privilege escalation in the console's role model, including a scoped viewer
  learning that a device or group they cannot see exists.
- Anything that lets an unauthenticated caller cause work — the validation gate
  is expensive on purpose, and expense is a denial-of-service surface.

Reports we will read but treat as lower priority: missing hardening headers
with no demonstrated impact, output from a scanner without an explanation of
why it matters here, and findings in a dependency that we do not reach.

## Known and accepted

`.govulncheck-exceptions` records vulnerabilities we have judged and accepted,
each with the condition that would make us revisit. If you find one of those
and disagree with the reasoning, that is a legitimate report — the entry is a
judgement, not a fact.

## What we ask you not to do

Do not access data that is not yours, do not degrade a service others depend
on, and do not use a finding to gain more access than you need to demonstrate
it. Beyond that, we are not going to write a list of rules for people who took
the trouble to tell us about a problem.
