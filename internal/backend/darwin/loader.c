#include "loader.h"

#include <dlfcn.h>
#include <pthread.h>
#include <stdlib.h>

struct comuse_loader {
    void *handle;
    int32_t (*abi_version)(uint32_t *);
    int32_t (*runtime_open)(const uint8_t *, size_t, uint64_t *, uint8_t *, size_t, size_t *);
    int32_t (*runtime_pump)(uint64_t, uint32_t);
    int32_t (*runtime_close)(uint64_t);
    int32_t (*request_start)(uint64_t, const uint8_t *, size_t, uint64_t, ComuseCompletionFn, uint64_t *);
    int32_t (*request_cancel)(uint64_t, uint64_t);
};

extern void goComuseNativeCompletion(uint64_t, uint64_t, int32_t, const uint8_t *, size_t);

static void comuse_callback(uint64_t callback_id, uint64_t request_id, int32_t status,
                            const uint8_t *bytes, size_t length) {
    goComuseNativeCompletion(callback_id, request_id, status, bytes, length);
}

int32_t comuse_loader_is_process_main(void) {
    return pthread_main_np() ? 1 : 0;
}

int32_t comuse_loader_open(const char *path, comuse_loader **loader_out) {
    if (!path || !loader_out) return -1;
    void *handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!handle) return -2;
    comuse_loader *loader = calloc(1, sizeof(*loader));
    if (!loader) { dlclose(handle); return -3; }
    loader->handle = handle;
#define LOAD(field, name) do { *(void **)(&loader->field) = dlsym(handle, name); if (!loader->field) goto invalid; } while (0)
    LOAD(abi_version, "comuse_abi_version");
    LOAD(runtime_open, "comuse_runtime_open");
    LOAD(runtime_pump, "comuse_runtime_pump");
    LOAD(runtime_close, "comuse_runtime_close");
    LOAD(request_start, "comuse_request_start");
    LOAD(request_cancel, "comuse_request_cancel");
#undef LOAD
    *loader_out = loader;
    return 0;
invalid:
    dlclose(handle);
    free(loader);
    return -4;
}

void comuse_loader_free(comuse_loader *loader) {
    if (!loader) return;
    if (loader->handle) dlclose(loader->handle);
    free(loader);
}

int32_t comuse_loader_abi_version(comuse_loader *loader, uint32_t *version_out) {
    if (!loader || !version_out) return -1;
    return loader->abi_version(version_out);
}

int32_t comuse_loader_runtime_open(comuse_loader *loader, const uint8_t *config,
                                   size_t config_length, uint64_t *runtime_out,
                                   uint8_t *resolved_scope_out, size_t resolved_scope_capacity,
                                   size_t *resolved_scope_length_out) {
    if (!loader) return -1;
    return loader->runtime_open(config, config_length, runtime_out, resolved_scope_out,
                                resolved_scope_capacity, resolved_scope_length_out);
}

int32_t comuse_loader_runtime_pump(comuse_loader *loader, uint64_t runtime_id, uint32_t timeout_ms) {
    if (!loader) return -1;
    return loader->runtime_pump(runtime_id, timeout_ms);
}

int32_t comuse_loader_runtime_close(comuse_loader *loader, uint64_t runtime_id) {
    if (!loader) return -1;
    return loader->runtime_close(runtime_id);
}

int32_t comuse_loader_request_start(comuse_loader *loader, uint64_t runtime_id,
                                    const uint8_t *request, size_t request_length,
                                    uint64_t callback_id, uint64_t *request_out) {
    if (!loader) return -1;
    return loader->request_start(runtime_id, request, request_length, callback_id,
                                 comuse_callback, request_out);
}

int32_t comuse_loader_request_cancel(comuse_loader *loader, uint64_t runtime_id,
                                     uint64_t request_id) {
    if (!loader) return -1;
    return loader->request_cancel(runtime_id, request_id);
}
