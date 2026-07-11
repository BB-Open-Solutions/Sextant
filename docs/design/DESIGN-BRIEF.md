# Design brief for Claude Design (or any designer)

What to feed a designer to build the Sextant console UI, and where it
lives. The app is functionally complete and server-rendered; the job is
a visual reskin, not new behaviour. Everything here is in this repo.

## Feed this, in order

1. **`docs/design/0002-ui-spec.md`** - THE primary artifact. Information
   architecture, every screen (purpose, data, states, actions, the role
   each needs), and the cross-cutting invariants (role-gated visibility,
   one-shot secret reveal, provenance display, catalog-driven settings
   form, auditable-commit framing, i18n + per-user timezone). It also
   says what a designer may change freely vs must keep.

2. **`internal/http/api/openapi.json`** - the frozen data contract. Every
   screen mirrors these 36 endpoints (list below). A SPA is a drop-in
   client; the designer can rebuild the front end entirely against this.

3. **`internal/http/web/templates/*.html`** - the CURRENT structure to
   reskin. 13 screens + `layout.html` (shell/nav) + `login.html`. These
   show exactly what data each page renders today; a designer restyles
   or replaces them.

4. **`internal/http/web/static/app.css`** - the current (minimal) styling
   to replace. `htmx.min.js` is the interaction layer (keep or drop for
   a SPA).

5. **`docs/capabilities.md`** - what the product does, in plain terms
   (11 capabilities), for the designer's mental model.

6. **`docs/design/0001,0003,0004,0005`** - specific flows that need
   thoughtful UX: SB/TPM2 enrollment wizard (0001), remote actions /
   red-zone (0004), and the future cells admin plane (0005). Feed these
   when designing those particular screens.

## The 13 screens (template -> purpose)

| Template | Screen | Role-gated |
|---|---|---|
| overview.html | fleet health at a glance | any |
| devices.html | device list (enroll, filter) | any (scoped) |
| device.html | device detail: identity, observed, effective config, manage, apps, **security posture wizard**, **remote-action red-zone**, facts | any (scoped) |
| groups.html | group tree (create/re-parent/remove) | any (scoped) |
| settings.html | catalog-driven settings per scope + apps | any (scoped) |
| policies.html | policies + assignments + filters editors | any (scoped) |
| changes.html | review queue (open/edit/build/merge) | org viewer |
| diff.html | unified diff before merge | org viewer |
| rollout.html | staged rollout + ring-plan editor | org viewer |
| access.html | role bindings + four-eyes toggle + LDAP picker | any (scoped) |
| audit.html | config commit trail + evidence export | org viewer |
| profile.html | personal settings: prefs (tz/lang) + own API tokens | any |
| layout.html | shell: top nav, profile menu, i18n | - |

## The API surface (what the UI drives)

```
GET    /api/v1/me                         who am I + roles per scope
GET    /api/v1/me/preferences             PUT to change tz/lang
GET    /api/v1/fleet                      whole document (scope-filtered)
GET    /api/v1/devices        /{tag}      list / detail
POST   /api/v1/devices                    enroll
PATCH  /api/v1/devices/{tag}              update fields
POST   /api/v1/devices/{tag}/retire  /reactivate  /credential
POST   /api/v1/devices/{tag}/intent       lock/wipe   DELETE = cancel
GET    /api/v1/status /{tag}   /facts/{tag}   observed plane
POST   /api/v1/groups   PATCH/DELETE /{name}
POST   /api/v1/settings  DELETE           per-scope config
PUT    /api/v1/apps                       package/flatpak/overlay lists
PUT    /api/v1/policies/{id}  DELETE      policies
POST   /api/v1/assignments    DELETE      bind policy -> scope
PUT    /api/v1/filters/{id}   DELETE      filters
GET/POST/DELETE /api/v1/access            role bindings
PUT    /api/v1/assurance                  four-eyes toggle
GET    /api/v1/changes /{id} /{id}/diff   review queue
POST   /api/v1/changes  /{id}/edits /submit /merge /abandon
GET    /api/v1/rollout    POST /tick  DELETE   PUT /rollout/plan
GET    /api/v1/tokens   POST   DELETE /{id}     personal tokens
GET    /api/v1/audit                      commit trail
GET    /api/v1/evidence?from=&to=         audit bundle download
GET    /api/v1/directory/groups?q=        LDAP group picker
```

## Non-negotiable invariants (tell the designer)

- **Role-gated visibility**: never show a control the user's role at that
  scope can't use. A group-A owner must not learn group B exists.
- **One-shot secret reveal**: device credentials and minted tokens appear
  exactly once, in a highlighted copyable panel, never in a URL. One
  reusable component, three places (enroll, re-issue, token mint).
- **Provenance everywhere**: a resolved setting shows which scope/policy
  set it and whether it is locked.
- **Catalog-driven settings**: the settings form is generated from
  catalog.json, not hand-built per option.
- **Auditable-commit framing**: every Apply/Save is a git commit that
  passed the validation gate; convey it quietly.
- **Destructive weightier than routine**: retire/remove/wipe use
  confirmation (wipe: type the tag). Red-zone styling for remote actions.
- **i18n + timezone**: all chrome through the message catalog (EN/NL);
  every timestamp in the user's zone.

## Free to change

Visual language, layout, nav grouping, componentry, and whether htmx
stays or a SPA replaces it. The `/api/v1` contract is frozen and complete
- a wild, distinctive front end is welcome as long as the invariants
above hold and every action maps to an existing endpoint.

## Run it live (for the designer)

Build/test in `README.md`; `--dev-auth` on loopback gives a synthetic
owner session against a seeded repo, so every screen renders with data
without an IdP.
