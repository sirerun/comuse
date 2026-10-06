#ifndef COMUSE_MACOS_COMUSE_H
#define COMUSE_MACOS_COMUSE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define COMUSE_ABI_VERSION 1u
#define COMUSE_MAX_REQUEST_BYTES 32768u
#define COMUSE_MAX_RESPONSE_BYTES 65536u

typedef void (*ComuseCompletionFn)(uint64_t callback_id,
                                   uint64_t request_id,
                                   int32_t status,
                                   const uint8_t *bytes,
                                   size_t length);

int32_t comuse_abi_version(uint32_t *version_out);
int32_t comuse_runtime_open(const uint8_t *config, size_t config_length,
                            uint64_t *runtime_out,
                            uint8_t *resolved_scope_out,
                            size_t resolved_scope_capacity,
                            size_t *resolved_scope_length_out);
int32_t comuse_runtime_pump(uint64_t runtime_id, uint32_t timeout_ms);
int32_t comuse_runtime_close(uint64_t runtime_id);
int32_t comuse_request_start(uint64_t runtime_id,
                             const uint8_t *request, size_t request_length,
                             uint64_t callback_id,
                             ComuseCompletionFn completion,
                             uint64_t *request_out);
int32_t comuse_request_cancel(uint64_t runtime_id, uint64_t request_id);

#ifdef __cplusplus
}
#endif

#endif
