# How a rollout ships

An update does not reach the whole fleet at once. It promotes through ordered
**waves**. Each wave is a group of devices; the next wave only starts once the
current one is converged healthy through its soak window. A wave can require a
manual approval gate - a human checkpoint that the update was tested.

The rollout page shows the plan as a ladder: each wave with its device count
(size it small first - a canary - then wider), soak, health floor and gate. Size
a wave by its group and order; refine each wave with the gates.

An organisation can require a gated test wave before any rollout starts; an
owner may skip it for a specific rollout, and that is logged.
