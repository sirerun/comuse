//go:build darwin && cgo

#include "loader.h"

#include <dlfcn.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

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

static pthread_mutex_t image_lock = PTHREAD_MUTEX_INITIALIZER;
static struct comuse_loader process_image;
static int process_image_pinned = 0;
static dev_t process_image_device;
static ino_t process_image_inode;

static void comuse_callback(uint64_t callback_id, uint64_t request_id, int32_t status,
                            const uint8_t *bytes, size_t length) {
    goComuseNativeCompletion(callback_id, request_id, status, bytes, length);
}

static int32_t image_identity(const char *path, dev_t *device, ino_t *inode) {
    struct stat info;
    if (!path || stat(path, &info) != 0 || !S_ISREG(info.st_mode)) return -1;
    *device = info.st_dev;
    *inode = info.st_ino;
    return 0;
}

static int32_t bind_symbols(struct comuse_loader *loader, void *handle) {
    loader->handle = handle;
#define LOAD(field, name) do { *(void **)(&loader->field) = dlsym(handle, name); if (!loader->field) return -1; } while (0)
    LOAD(abi_version, "comuse_abi_version");
    LOAD(runtime_open, "comuse_runtime_open");
    LOAD(runtime_pump, "comuse_runtime_pump");
    LOAD(runtime_close, "comuse_runtime_close");
    LOAD(request_start, "comuse_request_start");
    LOAD(request_cancel, "comuse_request_cancel");
#undef LOAD
    uint32_t version = 0;
    if (loader->abi_version(&version) != 0 || version != COMUSE_ABI_VERSION) return -2;
    Dl_info image_info;
    memset(&image_info, 0, sizeof(image_info));
    if (dladdr((void *)loader->abi_version, &image_info) == 0 || !image_info.dli_fname) return -3;
    dev_t actual_device;
    ino_t actual_inode;
    if (image_identity(image_info.dli_fname, &actual_device, &actual_inode) != 0) return -3;
    return 0;
}

int32_t comuse_loader_is_process_main(void) {
    return pthread_main_np() ? 1 : 0;
}

int32_t comuse_loader_open(const char *path, comuse_loader **loader_out) {
    if (!path || !loader_out) return -1;
    *loader_out = NULL;
    char *canonical = realpath(path, NULL);
    if (!canonical) return -2;
    dev_t candidate_device;
    ino_t candidate_inode;
    if (image_identity(canonical, &candidate_device, &candidate_inode) != 0) {
        free(canonical);
        return -2;
    }

    pthread_mutex_lock(&image_lock);
    if (process_image_pinned) {
        if (candidate_device != process_image_device || candidate_inode != process_image_inode) {
            pthread_mutex_unlock(&image_lock);
            free(canonical);
            return -5;
        }
        comuse_loader *wrapper = malloc(sizeof(*wrapper));
        if (!wrapper) {
            pthread_mutex_unlock(&image_lock);
            free(canonical);
            return -3;
        }
        *wrapper = process_image;
        *loader_out = wrapper;
        pthread_mutex_unlock(&image_lock);
        free(canonical);
        return 0;
    }

    void *handle = dlopen(canonical, RTLD_NOW | RTLD_LOCAL);
    free(canonical);
    if (!handle) {
        pthread_mutex_unlock(&image_lock);
        return -2;
    }
    struct comuse_loader candidate;
    memset(&candidate, 0, sizeof(candidate));
    int32_t bind_status = bind_symbols(&candidate, handle);
    if (bind_status != 0) {
        dlclose(handle);
        pthread_mutex_unlock(&image_lock);
        return bind_status == -2 ? -6 : -4;
    }
    Dl_info image_info;
    memset(&image_info, 0, sizeof(image_info));
    if (dladdr((void *)candidate.abi_version, &image_info) == 0 || !image_info.dli_fname ||
        image_identity(image_info.dli_fname, &process_image_device, &process_image_inode) != 0) {
        dlclose(handle);
        pthread_mutex_unlock(&image_lock);
        return -4;
    }
    if (process_image_device != candidate_device || process_image_inode != candidate_inode) {
        dlclose(handle);
        pthread_mutex_unlock(&image_lock);
        return -5;
    }
    process_image = candidate;
    process_image_pinned = 1;
    comuse_loader *wrapper = malloc(sizeof(*wrapper));
    if (!wrapper) {
        /* Keep the valid first image pinned even if wrapper allocation fails. */
        pthread_mutex_unlock(&image_lock);
        return -3;
    }
    *wrapper = process_image;
    *loader_out = wrapper;
    pthread_mutex_unlock(&image_lock);
    return 0;
}

void comuse_loader_free(comuse_loader *loader) {
    /* The one validated Swift image stays loaded for process lifetime. */
    free(loader);
}

int32_t comuse_loader_abi_version(comuse_loader *loader, uint32_t *version_out) {
    if (!loader || !version_out) return -1;
    return loader->abi_version(version_out);
}

int32_t comuse_loader_runtime_open(comuse_loader *loader,
                                   const uint8_t *config, size_t config_length,
                                   uint64_t *runtime_out,
                                   uint8_t *resolved_scope_out,
                                   size_t resolved_scope_capacity,
                                   size_t *resolved_scope_length_out) {
    if (!loader) return -1;
    return loader->runtime_open(config, config_length, runtime_out, resolved_scope_out,
                                resolved_scope_capacity, resolved_scope_length_out);
}

int32_t comuse_loader_runtime_pump(comuse_loader *loader, uint64_t runtime_id,
                                   uint32_t timeout_ms) {
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
    return loader->request_start(runtime_id, request, request_length, callback_id, comuse_callback, request_out);
}

int32_t comuse_loader_request_cancel(comuse_loader *loader, uint64_t runtime_id,
                                     uint64_t request_id) {
    if (!loader) return -1;
    return loader->request_cancel(runtime_id, request_id);
}
