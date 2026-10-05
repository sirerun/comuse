# Bridge client runtime lifetime

`Open`, `Pump`, and `Close` run on the actual process main OS thread. `Call`
runs on a worker goroutine while the main thread pumps. The Go `nativeMu`
serializes every native function call, cancellation, drain, and C wrapper
retirement so no call can use a stale function pointer during close.

After `runtime_open` succeeds, the C loader pins that Swift image for the rest
of the process. `Close` drains requests and retires the runtime, then frees the
C wrapper without calling `dlclose` on the activated image. The callback and
scheduled-work drain remains useful for request and token ownership, but it is
not treated as proof that every native closure stack frame has returned.

This spike allows one activated runtime/image per process. A second `Open`
returns a capacity error, even after `Close`; restart the host to upgrade or
load another image. No runtime unload safety claim is made.
