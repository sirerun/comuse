#ifndef COMUSE_SPIKE_H
#define COMUSE_SPIKE_H
#include <stddef.h>
#include <stdint.h>
#ifdef __cplusplus
extern "C" {
#endif
#define COMUSE_SPIKE_ABI_VERSION 1
#define COMUSE_SPIKE_MAX_REQUEST_BYTES (32u * 1024u)
#define COMUSE_SPIKE_MAX_RESPONSE_BYTES (64u * 1024u)
#define COMUSE_SPIKE_MAX_PUMP_MS 250u
enum comuse_spike_status {
    COMUSE_SPIKE_OK = 0, COMUSE_SPIKE_INVALID_ARGUMENT = 2,
    COMUSE_SPIKE_INVALID_REQUEST = 3, COMUSE_SPIKE_LIMIT_EXCEEDED = 4,
    COMUSE_SPIKE_UNKNOWN_HANDLE = 5, COMUSE_SPIKE_NOT_DRAINABLE = 6,
    COMUSE_SPIKE_INTERNAL_ERROR = 7, COMUSE_SPIKE_CAPACITY_EXCEEDED = 8,
    COMUSE_SPIKE_WRONG_THREAD = 9, COMUSE_SPIKE_RUNTIME_REQUIRED = 10,
    COMUSE_SPIKE_RUNTIME_CLOSED = 11
};
typedef void (*comuse_spike_completion_fn)(uint64_t handle, const uint8_t *bytes, size_t length, uint64_t callback_token);
uint32_t comuse_spike_abi_version(void);
int32_t comuse_spike_request_start(const uint8_t *request, size_t request_len, uint64_t callback_token, comuse_spike_completion_fn completion, uint64_t *out_handle);
int32_t comuse_spike_request_cancel(uint64_t handle);
int32_t comuse_spike_request_drain(uint64_t handle);
int32_t comuse_spike_runtime_open(uint64_t *out_runtime);
int32_t comuse_spike_runtime_pump(uint64_t runtime, uint32_t timeout_ms);
int32_t comuse_spike_runtime_close(uint64_t runtime);
#ifdef __cplusplus
}
#endif
#endif
