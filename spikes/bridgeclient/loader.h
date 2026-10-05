#ifndef COMUSE_BRIDGECLIENT_LOADER_H
#define COMUSE_BRIDGECLIENT_LOADER_H
#include <stddef.h>
#include <stdint.h>
#include "comuse_spike.h"
typedef struct comuse_bridge_library comuse_bridge_library;
int32_t bridge_library_open(const char *path, comuse_bridge_library **out_library);
void bridge_library_close(comuse_bridge_library *library);
int32_t bridge_is_main_thread(void);
int32_t bridge_runtime_open(comuse_bridge_library *library, uint64_t *out_runtime);
int32_t bridge_runtime_pump(comuse_bridge_library *library, uint64_t runtime, uint32_t timeout_ms);
int32_t bridge_runtime_close(comuse_bridge_library *library, uint64_t runtime);
int32_t bridge_request_start(comuse_bridge_library *library, const uint8_t *request, size_t request_len, uint64_t token, uint64_t *out_handle);
int32_t bridge_input_request_start(comuse_bridge_library *library, uint64_t runtime, const uint8_t *request, size_t request_len, uint64_t token, uint64_t *out_handle);
int32_t bridge_request_cancel(comuse_bridge_library *library, uint64_t handle);
int32_t bridge_request_drain(comuse_bridge_library *library, uint64_t handle);
#endif
