/* Independent CPA ABI 1 host probe. It deliberately does not include the
 * plugin's generated header or link to its Go implementation.
 *
 * ABI layout adapted from CPA and cpa-secret-manager (MIT):
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
#include <ctype.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>
#else
#include <dlfcn.h>
#endif

typedef struct { void *ptr; size_t len; } buffer;
typedef int (*host_call)(void *, const char *, const uint8_t *, size_t, buffer *);
typedef void (*release_buffer)(void *, size_t);
typedef struct {
    uint32_t abi_version;
    void *host_ctx;
    host_call call;
    release_buffer free_buffer;
} host_api;
typedef int (*plugin_call)(const char *, const uint8_t *, size_t, buffer *);
typedef struct {
    uint32_t abi_version;
    plugin_call call;
    release_buffer free_buffer;
    void (*shutdown)(void);
} plugin_api;
typedef int (*plugin_init)(const host_api *, plugin_api *);

static void check(int condition, const char *message) {
    if (!condition) {
        fprintf(stderr, "ABI probe: %s\n", message);
        exit(1);
    }
}

/* A bounded JSON reader for independent field/type assertions. next indexes
 * skip complete subtrees, so key ordering and whitespace do not affect checks. */
typedef struct { char kind; size_t start, len; int next; } token;
typedef struct {
    const char *raw;
    size_t len, pos;
    token tokens[256];
    int count;
} document;

static void whitespace(document *doc) {
    while (doc->pos < doc->len && isspace((unsigned char)doc->raw[doc->pos])) {
        doc->pos++;
    }
}

static int value(document *doc, unsigned depth) {
    whitespace(doc);
    check(depth < 24 && doc->count < 256 && doc->pos < doc->len, "invalid JSON bounds");
    int index = doc->count++;
    token *item = &doc->tokens[index];
    item->kind = doc->raw[doc->pos++];
    item->start = doc->pos - 1;
    if (item->kind == '{' || item->kind == '[') {
        char close = item->kind == '{' ? '}' : ']';
        whitespace(doc);
        if (doc->pos < doc->len && doc->raw[doc->pos] != close) {
            for (;;) {
                int child = value(doc, depth + 1);
                if (item->kind == '{') {
                    check(doc->tokens[child].kind == '"', "object key must be string");
                    whitespace(doc);
                    check(doc->pos < doc->len && doc->raw[doc->pos++] == ':', "missing JSON colon");
                    (void)value(doc, depth + 1);
                }
                whitespace(doc);
                check(doc->pos < doc->len, "unterminated JSON container");
                if (doc->raw[doc->pos] != ',') { break; }
                doc->pos++;
            }
        }
        check(doc->pos < doc->len && doc->raw[doc->pos++] == close, "missing JSON closer");
    } else if (item->kind == '"') {
        item->start = doc->pos;
        int ended = 0;
        while (doc->pos < doc->len) {
            unsigned char c = (unsigned char)doc->raw[doc->pos++];
            if (c == '"') { ended = 1; break; }
            check(c >= 0x20, "unescaped JSON control character");
            if (c == '\\') {
                check(doc->pos < doc->len, "incomplete JSON escape");
                doc->pos++;
            }
        }
        check(ended, "unterminated JSON string");
        item->len = doc->pos - item->start - 1;
    } else {
        while (doc->pos < doc->len && !isspace((unsigned char)doc->raw[doc->pos]) &&
               doc->raw[doc->pos] != ',' && doc->raw[doc->pos] != '}' && doc->raw[doc->pos] != ']') {
            doc->pos++;
        }
    }
    if (item->kind != '"') { item->len = doc->pos - item->start; }
    item->next = doc->count;
    return index;
}

static document parse(buffer response) {
    check(response.ptr != NULL && response.len != 0, "empty response buffer");
    document doc;
    memset(&doc, 0, sizeof(doc));
    doc.raw = (const char *)response.ptr;
    doc.len = response.len;
    (void)value(&doc, 0);
    whitespace(&doc);
    check(doc.pos == doc.len && doc.tokens[0].kind == '{', "response must be one JSON object");
    return doc;
}

static int equals(const document *doc, int index, const char *expected) {
    return index >= 0 && doc->tokens[index].len == strlen(expected) &&
           memcmp(doc->raw + doc->tokens[index].start, expected, strlen(expected)) == 0;
}

static int field(const document *doc, int object, const char *key) {
    check(object >= 0 && doc->tokens[object].kind == '{', "expected JSON object");
    int index = object + 1;
    while (index < doc->tokens[object].next) {
        int child = index + 1;
        if (equals(doc, index, key)) { return child; }
        index = doc->tokens[child].next;
    }
    return -1;
}

static int object_size(const document *doc, int object) {
    check(object >= 0 && doc->tokens[object].kind == '{', "expected JSON object for size");
    int count = 0;
    for (int i = object + 1; i < doc->tokens[object].next; i = doc->tokens[i + 1].next) { count++; }
    return count;
}

static void expect_error(buffer response, const char *code) {
    document doc = parse(response);
    check(equals(&doc, field(&doc, 0, "ok"), "false"), "negative scenario must fail");
    int error = field(&doc, 0, "error");
    check(equals(&doc, field(&doc, error, "code"), code), "unexpected RPC error code");
    int message = field(&doc, error, "message");
    check(message >= 0 && doc.tokens[message].kind == '"' && doc.tokens[message].len > 0,
          "error must retain diagnostic message");
}

static void expect_ok(buffer response) {
    document doc = parse(response);
    check(equals(&doc, field(&doc, 0, "ok"), "true"), "RPC success expected");
    check(field(&doc, 0, "error") == -1 && field(&doc, 0, "result") >= 0, "invalid success envelope");
}

static void emit(const char *label, buffer response) {
    printf("%s\t", label);
    check(fwrite(response.ptr, 1, response.len, stdout) == response.len, "writing probe response failed");
    check(putchar('\n') != EOF, "writing probe newline failed");
}

static unsigned callback_calls;
static int unexpected_host_call(void *ctx, const char *method, const uint8_t *request,
                                size_t len, buffer *response) {
    (void)ctx; (void)method; (void)request; (void)len; (void)response;
    callback_calls++;
    return -1;
}
static void unexpected_host_free(void *ptr, size_t len) {
    (void)ptr; (void)len;
    callback_calls++;
}

static void failed_call(plugin_api *api, const char *label, const char *method,
                        const uint8_t *raw, size_t len, int transport_error, const char *code) {
    buffer response = {(void *)(uintptr_t)1, (size_t)123};
    int rc = api->call(method, raw, len, &response);
    check(transport_error ? rc != 0 : rc == 0, "unexpected call return code");
    expect_error(response, code);
    emit(label, response);
    api->free_buffer(response.ptr, response.len);
}

static buffer call(plugin_api *api, const char *method, const char *request) {
    buffer response = {NULL, 0};
    check(api->call(method, (const uint8_t *)request, request ? strlen(request) : 0, &response) == 0,
          "RPC transport failed");
    expect_ok(response);
    return response;
}

int main(int argc, char **argv) {
    check(argc == 3, "usage: probe LIBRARY EXPECTED_VERSION");
#ifdef _WIN32
    int wide_len = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, argv[1], -1, NULL, 0);
    check(wide_len > 0, "library path is not UTF-8");
    wchar_t *wide_path = (wchar_t *)malloc((size_t)wide_len * sizeof(wchar_t));
    check(wide_path != NULL, "allocating library path failed");
    check(MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, argv[1], -1, wide_path, wide_len) > 0,
          "converting library path failed");
    HMODULE library = LoadLibraryW(wide_path);
    free(wide_path);
    if (library == NULL) { fprintf(stderr, "LoadLibraryW error: %lu\n", (unsigned long)GetLastError()); }
    check(library != NULL, "loading shared library failed");
    FARPROC symbol = GetProcAddress(library, "cliproxy_plugin_init");
    plugin_init init = NULL;
    _Static_assert(sizeof(init) == sizeof(symbol), "native function pointer width mismatch");
    memcpy(&init, &symbol, sizeof(init));
#else
    void *library = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (library == NULL) { fprintf(stderr, "dlopen: %s\n", dlerror()); }
    check(library != NULL, "loading shared library failed");
    void *symbol = dlsym(library, "cliproxy_plugin_init");
    plugin_init init = NULL;
    _Static_assert(sizeof(init) == sizeof(symbol), "native function pointer width mismatch");
    memcpy(&init, &symbol, sizeof(init));
#endif
    check(init != NULL, "cliproxy_plugin_init export missing");
    host_api host = {1, NULL, unexpected_host_call, unexpected_host_free};
    plugin_api api = {0, NULL, NULL, NULL};
    check(init(&host, NULL) != 0, "nil output API must fail");
    check(init(NULL, &api) != 0 && api.abi_version == 0 && api.call == NULL &&
          api.free_buffer == NULL && api.shutdown == NULL, "nil host must fail with cleared output");
    host.abi_version = 0;
    check(init(&host, &api) != 0 && api.call == NULL, "ABI zero must fail");
    host.abi_version = 2;
    check(init(&host, &api) != 0 && api.call == NULL, "newer ABI must fail");
    host.abi_version = 1;
    check(init(&host, &api) == 0, "ABI 1 handshake failed");
    check(api.abi_version == 1 && api.call != NULL && api.free_buffer != NULL && api.shutdown != NULL,
          "incomplete native API table");
    plugin_api duplicate = {0, NULL, NULL, NULL};
    check(init(&host, &duplicate) != 0 && duplicate.call == NULL, "duplicate init must fail");
    check(api.call("plugin.register", NULL, 0, NULL) != 0, "nil response must fail");
    api.free_buffer(NULL, 0);
    failed_call(&api, "nil-method", NULL, NULL, 0, 1, "invalid_method");
    failed_call(&api, "nil-positive-length", "plugin.register", NULL, 1, 1, "invalid_request");
    uint8_t byte = 0;
    failed_call(&api, "oversize", "plugin.register", &byte, SIZE_MAX, 1, "invalid_request");
    failed_call(&api, "c-int-overflow", "plugin.register", &byte, (size_t)2147483648ULL, 1, "invalid_request");
    failed_call(&api, "nil-request", "plugin.register", NULL, 0, 0, "invalid_request");
    failed_call(&api, "before-registration", "management.handle", NULL, 0, 0, "not_registered");
    const char *malformed = "{not-json}";
    failed_call(&api, "malformed-json", "plugin.register", (const uint8_t *)malformed, strlen(malformed), 0, "invalid_request");
    const uint8_t embedded_nul[] = {'{', '}', 0, '{', '}'};
    failed_call(&api, "embedded-nul", "plugin.register", embedded_nul, sizeof(embedded_nul), 0, "invalid_request");
    const char *schema = "{\"schema_version\":7}";
    failed_call(&api, "schema-mismatch", "plugin.register", (const uint8_t *)schema, strlen(schema), 0, "incompatible_schema");
    const char *empty = "{}";
    failed_call(&api, "unknown-method", "workbuddy.unknown", (const uint8_t *)empty, 2, 0, "unknown_method");
    failed_call(&api, "no-plugin-identifier", "plugin.identifier", (const uint8_t *)empty, 2, 0, "unknown_method");
    failed_call(&api, "no-auth-identifier", "auth.identifier", (const uint8_t *)empty, 2, 0, "unknown_method");
    failed_call(&api, "no-executor-identifier", "executor.identifier", (const uint8_t *)empty, 2, 0, "unknown_method");

    buffer registration = call(&api, "plugin.register", "{\"config_yaml\":null,\"schema_version\":6}");
    document registered = parse(registration);
    int result = field(&registered, 0, "result");
    check(equals(&registered, field(&registered, result, "schema_version"), "6"), "registration schema must be 6");
    int meta = field(&registered, result, "metadata");
    check(equals(&registered, field(&registered, meta, "Name"), "cpa-workbuddy"), "incorrect plugin identity");
    check(equals(&registered, field(&registered, meta, "Version"), argv[2]), "incorrect embedded product version");
    int author = field(&registered, meta, "Author");
    int repository = field(&registered, meta, "GitHubRepository");
    check(author >= 0 && registered.tokens[author].len > 0 && repository >= 0 &&
          registered.tokens[repository].len > 0, "host requires author and source metadata");
    int capabilities = field(&registered, result, "capabilities");
    check(object_size(&registered, capabilities) == 1 &&
          equals(&registered, field(&registered, capabilities, "management_api"), "true"),
          "only real management capability may be advertised");
    emit("registration", registration);
    void *registration_snapshot = malloc(registration.len);
    check(registration_snapshot != NULL, "allocating ownership snapshot failed");
    memcpy(registration_snapshot, registration.ptr, registration.len);

    buffer routes = call(&api, "management.register", "{\"BasePath\":\"/v0/management\",\"ResourceBasePath\":\"/v0/resource/plugins/cpa-workbuddy\"}");
    document route_doc = parse(routes);
    int route_result = field(&route_doc, 0, "result");
    int route_list = field(&route_doc, route_result, "routes");
    check(route_list >= 0 && route_doc.tokens[route_list].kind == '[', "management routes must be array");
    int route = route_list + 1;
    check(route < route_doc.tokens[route_list].next && route_doc.tokens[route].next == route_doc.tokens[route_list].next,
          "expected exactly one real management route");
    check(equals(&route_doc, field(&route_doc, route, "Method"), "GET") &&
          equals(&route_doc, field(&route_doc, route, "Path"), "/v0/management/plugins/cpa-workbuddy/status"),
          "incorrect status route");
    emit("routes", routes);
    api.free_buffer(routes.ptr, routes.len);

    const char *status_request = "{\"Method\":\"GET\",\"Path\":\"/v0/management/plugins/cpa-workbuddy/status\"}";
    buffer status = call(&api, "management.handle", status_request);
    document status_doc = parse(status);
    int status_result = field(&status_doc, 0, "result");
    check(equals(&status_doc, field(&status_doc, status_result, "StatusCode"), "200"), "status must be HTTP 200");
    check(field(&status_doc, status_result, "Body") >= 0, "missing base64 management body");
    emit("status", status);
    api.free_buffer(status.ptr, status.len);
    const char *wrong_path = "{\"Method\":\"GET\",\"Path\":\"/missing\"}";
    failed_call(&api, "wrong-path", "management.handle", (const uint8_t *)wrong_path, strlen(wrong_path), 0, "route_not_found");
    const char *wrong_method = "{\"Method\":\"POST\",\"Path\":\"/v0/management/plugins/cpa-workbuddy/status\"}";
    failed_call(&api, "wrong-http-method", "management.handle", (const uint8_t *)wrong_method, strlen(wrong_method), 0, "method_not_allowed");
    buffer quiesce = call(&api, "plugin.quiesce", "{}");
    api.free_buffer(quiesce.ptr, quiesce.len);
    failed_call(&api, "quiesced", "management.handle", (const uint8_t *)status_request, strlen(status_request), 0, "plugin_quiesced");
    buffer reconfigure = call(&api, "plugin.reconfigure", "{\"schema_version\":6,\"config_yaml\":\"eDogMQo=\"}");
    api.free_buffer(reconfigure.ptr, reconfigure.len);
    status = call(&api, "management.handle", status_request);
    api.free_buffer(status.ptr, status.len);
    /* Keep an earlier response alive across later calls and shutdown: the
     * plugin may neither reuse nor release buffers still owned by the host. */
    expect_ok(registration);
    buffer shutdown = call(&api, "plugin.shutdown", "{}");
    api.free_buffer(shutdown.ptr, shutdown.len);
    failed_call(&api, "rpc-shutdown", "management.handle", NULL, 0, 0, "plugin_shutdown");
    api.shutdown();
    api.shutdown();
    expect_ok(registration);
    check(memcmp(registration_snapshot, registration.ptr, registration.len) == 0,
          "plugin changed a response still owned by the host");
    free(registration_snapshot);
    api.free_buffer(registration.ptr, registration.len);
    failed_call(&api, "native-shutdown", "plugin.register", NULL, 0, 0, "plugin_shutdown");
    /* Also exercise native shutdown without a preceding RPC shutdown and a
     * host with no callbacks: neither management nor identity requires them. */
    host.call = NULL;
    host.free_buffer = NULL;
    check(init(&host, &api) == 0, "reinitializing after shutdown failed");
    registration = call(&api, "plugin.register", "{\"schema_version\":6}");
    api.shutdown();
    failed_call(&api, "direct-native-shutdown", "management.handle", NULL, 0, 0, "plugin_shutdown");
    expect_ok(registration);
    api.free_buffer(registration.ptr, registration.len);
    check(callback_calls == 0, "identity capability unexpectedly used host callbacks");
#ifdef _WIN32
    check(FreeLibrary(library) != 0, "FreeLibrary failed");
#else
    check(dlclose(library) == 0, "dlclose failed");
#endif
    check(fflush(stdout) == 0, "flushing probe output failed");
    return 0;
}
