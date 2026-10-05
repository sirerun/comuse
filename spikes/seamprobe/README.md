# Legacy hello seamprobe lifetime

The legacy hello probe drains its callback and request before clearing the C
function pointers. Once ABI validation succeeds, however, the loaded Swift image
is pinned until process exit. The Swift hello queue's completion bookkeeping
does not prove that every native closure stack frame has returned, so
`seam_close` deliberately does not `dlclose` an activated image. One activated
image is allowed per process; restart the probe process to load an upgrade.
Loads that fail symbol or ABI validation remain pre-activation and are closed.
