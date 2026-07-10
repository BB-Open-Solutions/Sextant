# Example repo-defined overlay module: engineer-authored custom work the
# console can only SELECT by name (ADR 0005 tier 2).
{ lib, ... }:
{
  # Demo effect: a recognizable motd.
  users.motd = "Managed by Sextant - example organisation";
}
