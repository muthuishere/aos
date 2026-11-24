// Window layouts: put several apps on one monitor in one call.

const ZONES = {
  1: ["1:1"],
  2: ["1A", "1B"],
  3: ["3:1", "3:2", "3:3"],
  4: ["2A", "2B", "2C", "2D"],
}

/**
 * Tile 1 to 4 apps on a monitor: 1 fills it, 2 are left and right halves,
 * 3 are thirds, 4 are quarters (top-left, top-right, bottom-left, bottom-right).
 * apps: ["Code", "Safari"]; monitor: 1-based, default 1.
 * Returns [{app, zone}] in the order given; rejects when a move fails.
 */
export async function tile(apps, monitor = 1) {
  const zones = ZONES[apps.length]
  if (!zones) throw Error("tile takes 1 to 4 apps, got " + apps.length)
  await Promise.all(apps.map((app, i) =>
    ops["window-move"]({ args: [app], zone: zones[i], monitor })))
  return apps.map((app, i) => ({ app, zone: zones[i] }))
}

/**
 * Put two apps side by side (left, right) on a monitor.
 * Returns [{app, zone}].
 */
export async function sideBySide(left, right, monitor = 1) {
  return tile([left, right], monitor)
}
