# Sextant on NixOS, in a VM that boots the module and uses the console.
#
# The path exists so somebody can run their own fleet from a single NixOS
# host. Nothing proved that it worked: the module used to pass only --addr,
# which started a console that could not be given a repository or the write
# path, and the flake had no test that would have noticed.
{ self, pkgs }:
pkgs.testers.runNixOSTest {
  name = "sextant-console-on-nixos";

  nodes.machine = { config, pkgs, ... }: {
    imports = [ self.nixosModules.default ];

    services.postgresql = {
      enable = true;
      ensureDatabases = [ "sextant" ];
      ensureUsers = [{
        name = "sextant";
        ensureDBOwnership = true;
      }];
    };

    services.sextant = {
      enable = true;
      addr = "127.0.0.1:8080";
      # A fixed user, because the console WRITES this repository and the
      # operator has to be able to prepare it. With a DynamicUser the uid does
      # not exist until the unit starts, and StateDirectory then lives under
      # /var/lib/private where git refuses to work on a tree it does not own.
      # That is what the first version of this test hit.
      user = "sextant";
      repo = "/srv/sextant/overlay";
      write = true;
      # A VM has no gate-runner and cannot evaluate this overlay, so the test
      # says so out loud rather than pretending: this is the acknowledged
      # dev combination, and the module's assertion is what forces the
      # acknowledgement.
      gate = "none";
      allowUnvalidated = true;
      secureCookies = false;
      extraArgs = [ "--dev-auth" ];
      environmentFile = "/etc/sextant.env";
    };

    # The console connects over TCP, so only that line is added. Overriding
    # the whole file with mkForce broke postgres own admin connection
    # (peer auth for user "postgres"), which is a good reminder that a
    # test-only shortcut can create the failure it was meant to avoid.
    services.postgresql.authentication = ''
      host all all 127.0.0.1/32 trust
    '';

    environment.etc."sextant.env".text = ''
      SEXTANT_PG_DSN=postgres://sextant@127.0.0.1/sextant?sslmode=disable
      SEXTANT_CHECKIN_TOKEN=vm-test
    '';

    environment.systemPackages = [ pkgs.git pkgs.curl pkgs.jq ];
  };

  testScript = ''
    machine.wait_for_unit("postgresql.service")

    # The overlay is the config plane and has to exist before the service can
    # commit to it. StateDirectory gives us the parent; this is the repo.
    # Prepare the overlay the way an operator would: put it somewhere of your
    # own choosing and give it to the service's user.
    machine.succeed("mkdir -p /srv/sextant/overlay")
    machine.succeed(
        "cp -r ${self}/examples/overlay/. /srv/sextant/overlay/ && "
        "chmod -R u+w /srv/sextant/overlay"
    )
    machine.succeed("chown -R sextant:sextant /srv/sextant/overlay")
    # Seed it AS the service user. The module tmpfiles rule already made the
    # directory theirs, so root git refuses to touch it - which is the
    # protection working, not a problem to route around.
    machine.succeed(
        "runuser -u sextant -- sh -c 'cd /srv/sextant/overlay && "
        "git init -q -b main && "
        "git -c user.name=t -c user.email=t@t add -A && "
        "git -c user.name=t -c user.email=t@t commit -qm seed'"
    )

    machine.succeed("systemctl restart sextant.service")
    machine.wait_for_unit("sextant.service")
    machine.wait_for_open_port(8080)

    # It answers, and it answers with a real page rather than an error shell.
    machine.succeed("curl -fsS http://127.0.0.1:8080/devices | grep -q '</html>'")

    # The observed plane is wired: without a database this endpoint is a 503.
    machine.succeed("curl -fsS -o /dev/null http://127.0.0.1:8080/station")

    # And the write path actually writes. This is the half the old module
    # could not do at all.
    machine.succeed(
        "curl -fsS -c /tmp/c -o /dev/null http://127.0.0.1:8080/settings && "
        "curl -fsS -b /tmp/c -o /dev/null -X POST http://127.0.0.1:8080/settings "
        "--data-urlencode csrf=dev-csrf --data-urlencode scope=org "
        "--data-urlencode v:desktop=gnome"
    )
    machine.succeed(
        "grep -q gnome /srv/sextant/overlay/fleet.json"
    )
  '';
}
