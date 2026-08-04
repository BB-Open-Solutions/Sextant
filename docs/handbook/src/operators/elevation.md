# Approving a request for privilege

Somebody on a fleet laptop tries to do one privileged thing - install a printer
driver, change a network setting - and the system asks for an administrator.
This is where that request lands.

## The problem it solves

polkit's answer to "you may not do this" is a dialog asking for an
**administrator's password**. On a managed fleet machine that means the local
admin account. Away from the office, that is not a slower path - it is no path
at all.

The predictable outcome is that somebody shares the admin password to get the
user unstuck, and then it is shared again, until it has stopped being a secret.

So the request becomes something an administrator answers **centrally**, and it
is logged.

## What an approval is, and what it is not

Sextant **decides**. It never asserts.

The grant does not come from the console and cannot: polkit will not let an
agent vouch for an identity. The answer travels through polkit's own setuid
helper, which runs PAM, and PAM turns the answer into an authentication - or
does not. The console's role is to say yes or no; the device's own machinery
enforces it.

This matters for how you read the request.

## Approve on who and where, not on what

Each request carries four things:

| | |
|---|---|
| **Device** | Taken from the device's authenticated check-in, never from the request itself - otherwise any device could raise a request in another's name. |
| **User** | Who is asking. |
| **Action** | What the session says it is trying to do. |
| **Reason** | What the user typed. The only field a human wrote, and usually the most useful. |

**The action is context, not proof.** PAM is never told the polkit action id, so
the device's own session supplies that string - and a session is not a
trustworthy narrator about itself. Approve on the strength of *who* is asking
and *where*, both of which are established by the device's authenticated
check-in, and read the action as what the user *says* they are doing.

## Five minutes, and why it is short

A request waits **five minutes** for an answer.

That is deliberately short. Somebody is standing in front of a frozen dialog for
the whole window, so a generous timeout is not generosity. Five minutes is long
enough for an administrator who is watching, and short enough that a user who
gets no answer finds out while they still remember what they were trying to do.

## Four outcomes, and one of them is not a decision

- **Approved** - the console said yes; PAM completes the authentication.
- **Denied** - the console said no.
- **Pending** - waiting, within the window.
- **Expired** - nobody answered in time.

Expired is a distinct outcome rather than a flavour of denied, on purpose. "We
said no" and "nobody was there" are different conversations, and a list of
expired requests is a **staffing problem, not a policy one**. If they are
piling up, the answer is not to tighten the rules.

Expiry is derived from the clock rather than written down, so a request cannot
sit Pending for ever because whatever was supposed to expire it died.

## Where to find them

The **Requests** page in the console. Every decision records who made it and
when, and lands in the audit log like any other action.
