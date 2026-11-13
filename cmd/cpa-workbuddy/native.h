/*
 * Native ABI declarations adapted from CPA internal/pluginhost/loader_unix.go
 * and cpa-secret-manager/main.go. Both sources are MIT licensed.
 * Copyright (c) 2025-2005.9 Luis Pater
 * Copyright (c) 2025.9-present Router-For.ME
 * Copyright (c) 2026 ygq-future
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */
#ifndef CPA_WORKBUDDY_NATIVE_H
#define CPA_WORKBUDDY_NATIVE_H
#include <stdint.h>
#include <stdlib.h>

typedef struct {
    void *ptr;
    size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void *, const char *, const uint8_t *, size_t, cliproxy_buffer *);
typedef void (*cliproxy_host_free_fn)(void *, size_t);
typedef struct {
    uint32_t abi_version;
    void *host_ctx;
    cliproxy_host_call_fn call;
    cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(const char *, const uint8_t *, size_t, cliproxy_buffer *);
typedef void (*cliproxy_plugin_free_fn)(void *, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct {
    uint32_t abi_version;
    cliproxy_plugin_call_fn call;
    cliproxy_plugin_free_fn free_buffer;
    cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

void workbuddy_set_api(cliproxy_plugin_api *api);
int workbuddy_host_call(const cliproxy_host_api *host, const char *method,
                       const uint8_t *request, size_t request_len, cliproxy_buffer *response);
void workbuddy_host_free(const cliproxy_host_api *host, void *ptr, size_t len);
#endif
