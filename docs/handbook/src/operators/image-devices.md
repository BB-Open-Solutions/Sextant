# Image a device from the console

Once a station is reporting, imaging a device is driven from the console.

1. **Discover** - boot the target device on the station's network. It appears
   under **Enrollment** for that station.
2. **Dispatch** - on the Enrollment page, pick the target (or a whole rack in
   batch), choose its hardware profile (suggested from the make and model), and
   dispatch imaging. This creates the device record, captures its hardware
   specs, and queues an image job. The device stays visible with live status.
3. **Execute** - the station runner claims the job, resolves the target's IP
   from its DHCP lease, runs `nixos-anywhere` for that device's generated
   configuration, and bakes the device's one-time agent credential into the
   image. It reports progress back: pending -> imaging -> installed (or failed).
4. **Converge** - the imaged device boots, checks in, and converges its
   configuration. It now shows on the device page with its facts and posture.

The guided flow shows brand-specific steps per hardware profile (firmware key,
Secure Boot, disk layout) so an operator is walked through the process, and the
whole run is auditable.
