#ifndef COMUSE_DARWIN_LOADER_H
#define COMUSE_DARWIN_LOADER_H

#include <stddef.h>
#include <stdint.h>
#include "comuse.h"

typedef struct comuse_loader comuse_loader;

int32_t comuse_loader_is_process_main(void);
int32_t comuse_loader_open(const char *path, comuse_loader **loader_out);
void comuse_loader_free(comuse_loader *loader);
int32_t comuse_loader_abi_version(comuse_loader *loader, uint32_t *version_out);
int32_t comuse_loader_runtime_open(comuse_loader *loader,
                                   const uint8_t *config, size_t config_length,
                                   uint64_t *runtime_out,
                                   uint8_t *resolved_scope_out,
                                   size_t resolved_scope_capacity,
                                   size_t *resolved_scope_length_out);
int32_t comuse_loader_runtime_pump(comuse_loader *loader, uint64_t runtime_id,
                                   uint32_t timeout_ms);
int32_t comuse_loader_runtime_close(comuse_loader *loader, uint64_t runtime_id);
int32_t comuse_loader_request_start(comuse_loader *loader, uint64_t runtime_id,
                                    const uint8_t *request, size_t request_length,
                                    uint64_t callback_id, uint64_t *request_out);
int32_t comuse_loader_request_cancel(comuse_loader *loader, uint64_t runtime_id,
                                     uint64_t request_id);

#endif
