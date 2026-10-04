//go:build darwin && cgo

#include <dlfcn.h>
#include <stdint.h>
#include "comuse_spike.h"

extern void goComuseCompletion(uint64_t, const uint8_t *, size_t, uint64_t);
typedef uint32_t (*version_fn)(void);
typedef int32_t (*start_fn)(const uint8_t *, size_t, uint64_t, comuse_spike_completion_fn, uint64_t *);
typedef int32_t (*handle_fn)(uint64_t);
static void *library_handle;
static version_fn version_call;
static start_fn start_call;
static handle_fn cancel_call;
static handle_fn drain_call;
void seam_close(void);

static void completion(uint64_t handle, const uint8_t *bytes, size_t length, uint64_t token) {
    goComuseCompletion(handle, bytes, length, token);
}
int32_t seam_open(const char *path) {
    library_handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!library_handle) return -1;
    version_call = (version_fn)dlsym(library_handle, "comuse_spike_abi_version");
    start_call = (start_fn)dlsym(library_handle, "comuse_spike_request_start");
    cancel_call = (handle_fn)dlsym(library_handle, "comuse_spike_request_cancel");
    drain_call = (handle_fn)dlsym(library_handle, "comuse_spike_request_drain");
    if (!version_call || !start_call || !cancel_call || !drain_call) { seam_close(); return -2; }
    return 0;
}
void seam_close(void) {
    if (library_handle) dlclose(library_handle);
    library_handle = 0; version_call = 0; start_call = 0; cancel_call = 0; drain_call = 0;
}
uint32_t seam_version(void) { return version_call ? version_call() : 0; }
int32_t seam_start(const uint8_t *request, size_t length, uint64_t token, uint64_t *handle) {
    return start_call ? start_call(request, length, token, completion, handle) : -1;
}
int32_t seam_cancel(uint64_t handle) { return cancel_call ? cancel_call(handle) : -1; }
int32_t seam_drain(uint64_t handle) { return drain_call ? drain_call(handle) : -1; }
