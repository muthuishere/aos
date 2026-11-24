// Read the machine's state in one call: windows, pointer, busiest processes, screenshots.

/**
 * One read-only picture of the machine, fetched in parallel.
 * Returns {windows, focused, mouse: {x, y}, busiest}: windows is the window
 * list (App, Title, Bounds, Monitor, Focused), focused is the focused window
 * or null, busiest is the `top` (default 5) processes by CPU {pid, name, cpu, memory_mb}.
 */
export async function snapshot(top = 5) {
  const [windows, pos, busiest] = await Promise.all([
    ops["window-list"]({}),
    ops["mouse-position"]({}),
    ops["process-list"]({ sort: "cpu", top }),
  ])
  const xy = String(pos).trim().split(" ")
  const focused = windows.filter(w => w.Focused)
  return {
    windows,
    focused: focused.length > 0 ? focused[0] : null,
    mouse: { x: Number(xy[0]), y: Number(xy[1]) },
    busiest,
  }
}

/**
 * Screenshot several apps' windows in parallel, one PNG each in `dir`
 * (default /tmp). Returns [{app, path}]; rejects when any capture fails.
 */
export async function shotEach(apps, dir = "/tmp") {
  const paths = apps.map(app => dir + "/" + app.split(" ").join("-") + ".png")
  await Promise.all(apps.map((app, i) =>
    ops["capture-screenshot"]({ args: ["window"], app, out: paths[i] })))
  return apps.map((app, i) => ({ app, path: paths[i] }))
}
