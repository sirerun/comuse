//go:build darwin && cgo

#include "loader.h"
#include <dlfcn.h>
#include <pthread.h>
#include <stdlib.h>

struct comuse_bridge_library {
    void *image;
    int activated;
    uint32_t (*abi_version)(void);
    int32_t (*request_start)(const uint8_t *, size_t, uint64_t, comuse_spike_completion_fn, uint64_t *);
    int32_t (*request_cancel)(uint64_t);
    int32_t (*request_drain)(uint64_t);
    int32_t (*runtime_open)(uint64_t *);
    int32_t (*runtime_pump)(uint64_t, uint32_t);
    int32_t (*runtime_close)(uint64_t);
};

// One activated Swift image remains mapped until process exit. A queued Swift
// closure may still be returning through image code after its completion
// bookkeeping changes; dlclose cannot prove that stack frame has exited.
static void *pinned_image = NULL;
static pthread_mutex_t loader_mutex = PTHREAD_MUTEX_INITIALIZER;

extern void goBridgeCompletion(uint64_t, const uint8_t *, size_t, uint64_t);
static void bridge_completion(uint64_t handle, const uint8_t *bytes, size_t length, uint64_t token) {
    goBridgeCompletion(handle, bytes, length, token);
}
#define LOAD_SYMBOL(target, image, symbol) do { *(void **)(&(target)) = dlsym((image), (symbol)); if (!(target)) goto missing_symbol; } while (0)

int32_t bridge_library_open(const char *path, comuse_bridge_library **out_library) {
    if (!path || !out_library) return COMUSE_SPIKE_INVALID_ARGUMENT;
    pthread_mutex_lock(&loader_mutex);
    if (pinned_image) {
        pthread_mutex_unlock(&loader_mutex);
        return COMUSE_SPIKE_CAPACITY_EXCEEDED;
    }
    comuse_bridge_library *library = calloc(1, sizeof(*library));
    if (!library) {
        pthread_mutex_unlock(&loader_mutex);
        return COMUSE_SPIKE_INTERNAL_ERROR;
    }
    library->image = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!library->image) {
        free(library);
        pthread_mutex_unlock(&loader_mutex);
        return COMUSE_SPIKE_INTERNAL_ERROR;
    }
    LOAD_SYMBOL(library->abi_version, library->image, "comuse_spike_abi_version");
    LOAD_SYMBOL(library->request_start, library->image, "comuse_spike_request_start");
    LOAD_SYMBOL(library->request_cancel, library->image, "comuse_spike_request_cancel");
    LOAD_SYMBOL(library->request_drain, library->image, "comuse_spike_request_drain");
    LOAD_SYMBOL(library->runtime_open, library->image, "comuse_spike_runtime_open");
    LOAD_SYMBOL(library->runtime_pump, library->image, "comuse_spike_runtime_pump");
    LOAD_SYMBOL(library->runtime_close, library->image, "comuse_spike_runtime_close");
    if (library->abi_version() != COMUSE_SPIKE_ABI_VERSION) goto incompatible;
    *out_library = library;
    pthread_mutex_unlock(&loader_mutex);
    return COMUSE_SPIKE_OK;
missing_symbol:
    dlclose(library->image);
    free(library);
    pthread_mutex_unlock(&loader_mutex);
    return COMUSE_SPIKE_INTERNAL_ERROR;
incompatible:
    dlclose(library->image);
    free(library);
    pthread_mutex_unlock(&loader_mutex);
    return COMUSE_SPIKE_INVALID_REQUEST;
}
void bridge_library_close(comuse_bridge_library *library) {
    if (!library) return;
    pthread_mutex_lock(&loader_mutex);
    if (library->image && !library->activated) dlclose(library->image);
    free(library);
    pthread_mutex_unlock(&loader_mutex);
}
int32_t bridge_is_main_thread(void) { return pthread_main_np(); }
int32_t bridge_runtime_open(comuse_bridge_library *library, uint64_t *out_runtime) {
    if (!library) return COMUSE_SPIKE_INVALID_ARGUMENT;
    pthread_mutex_lock(&loader_mutex);
    if (pinned_image) {
        pthread_mutex_unlock(&loader_mutex);
        return COMUSE_SPIKE_CAPACITY_EXCEEDED;
    }
    int32_t status = library->runtime_open(out_runtime);
    if (status == COMUSE_SPIKE_OK) {
        library->activated = 1;
        pinned_image = library->image;
    }
    pthread_mutex_unlock(&loader_mutex);
    return status;
}
int32_t bridge_runtime_pump(comuse_bridge_library *library, uint64_t runtime, uint32_t timeout_ms) {
    return library ? library->runtime_pump(runtime, timeout_ms) : COMUSE_SPIKE_INVALID_ARGUMENT;
}
int32_t bridge_runtime_close(comuse_bridge_library *library, uint64_t runtime) {
    return library ? library->runtime_close(runtime) : COMUSE_SPIKE_INVALID_ARGUMENT;
}
int32_t bridge_request_start(comuse_bridge_library *library, const uint8_t *request, size_t length, uint64_t token, uint64_t *out_handle) {
    return library ? library->request_start(request, length, token, bridge_completion, out_handle) : COMUSE_SPIKE_INVALID_ARGUMENT;
}
int32_t bridge_request_cancel(comuse_bridge_library *library, uint64_t handle) {
    return library ? library->request_cancel(handle) : COMUSE_SPIKE_INVALID_ARGUMENT;
}
int32_t bridge_request_drain(comuse_bridge_library *library, uint64_t handle) {
    return library ? library->request_drain(handle) : COMUSE_SPIKE_INVALID_ARGUMENT;
}
