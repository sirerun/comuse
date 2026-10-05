import Foundation

@_cdecl("comuse_spike_abi_version")
public func comuse_spike_abi_version() -> UInt32 { 1 }

@_cdecl("comuse_spike_request_start")
public func comuse_spike_request_start(
    _ bytes: UnsafePointer<UInt8>?,
    _ length: Int,
    _ token: UInt64,
    _ callback: (@convention(c) (UInt64, UnsafePointer<UInt8>?, Int, UInt64) -> Void)?,
    _ outHandle: UnsafeMutablePointer<UInt64>?
) -> Int32 {
    startRequest(bytes, length, token, callback, outHandle)
}

@_cdecl("comuse_spike_request_cancel")
public func comuse_spike_request_cancel(_ handle: UInt64) -> Int32 {
    cancelRequest(handle)
}

@_cdecl("comuse_spike_request_drain")
public func comuse_spike_request_drain(_ handle: UInt64) -> Int32 {
    drainRequest(handle)
}

@_cdecl("comuse_spike_runtime_open")
public func comuse_spike_runtime_open(_ outRuntime: UnsafeMutablePointer<UInt64>?) -> Int32 {
    openRuntime(outRuntime)
}

@_cdecl("comuse_spike_runtime_pump")
public func comuse_spike_runtime_pump(_ runtime: UInt64, _ timeoutMilliseconds: UInt32) -> Int32 {
    pumpRuntime(runtime, timeoutMilliseconds: timeoutMilliseconds)
}

@_cdecl("comuse_spike_runtime_close")
public func comuse_spike_runtime_close(_ runtime: UInt64) -> Int32 {
    closeRuntime(runtime)
}
