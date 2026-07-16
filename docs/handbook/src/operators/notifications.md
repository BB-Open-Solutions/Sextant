# Notifications

The bell icon in the top app bar opens **Notifications**: an in-app inbox for
things that need your attention - a change ready for review, a wave awaiting
approval, an incident, and similar events. An unread count badges the bell.

- Click a notification to jump straight to the thing it is about (the
  change, the device, the rollout) and mark it read at the same time.
- **Mark all read** clears the unread badge without visiting each item.
- An empty inbox and "notifications unavailable" (the backing store is not
  configured for this deployment) are both shown plainly rather than as an
  error.

In-app notifications work with no extra setup. To also receive them by
e-mail, an organisation owner configures SMTP once under
**Organisation -> E-mail (SMTP)** - see
[Install and configure Sextant](./deploy.md#notification-e-mail-smtp) for the
setup details (host/port/from, and the two ways to hold the password). Mail
delivery is additive: turning it on does not change what shows up in the
in-app inbox, only whether the same events also arrive by e-mail.

Push notifications are not implemented yet.

## Troubleshooting

**Notifications never arrive at all (in-app).**
The page shows "unavailable" rather than an empty inbox when the
notifications backing store is not configured for this deployment - that is
a deploy-time gap, not a per-user setting.

**In-app notifications work but e-mail does not.**
Check the SMTP configuration under **Organisation -> E-mail (SMTP)**: host,
port and the password source (a registered secret reference, or a typed
password sealed with `SEXTANT_SECRET_KEY`). If `SEXTANT_SECRET_KEY` is not
set on this deployment, the typed-password option is disabled and the
console says so - use a secret reference instead.

**A notification links to something that no longer exists.**
The event still happened (the notification records history), but its target
(a since-abandoned change, a completed rollout) may no longer show the same
detail page - this is expected once enough time has passed.
