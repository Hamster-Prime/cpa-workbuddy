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
#include <pthread.h>
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

#ifdef _WIN32
typedef HANDLE event;
typedef HANDLE thread;
#define THREAD_RESULT DWORD WINAPI
#define THREAD_RETURN 0
typedef DWORD (WINAPI *thread_entry)(void *);
static void event_init(event *item) {
    *item = CreateEventW(NULL, TRUE, FALSE, NULL);
    check(*item != NULL, "creating probe event failed");
}
static void event_signal(event *item) { check(SetEvent(*item) != 0, "signaling probe event failed"); }
static void event_wait(event *item) { check(WaitForSingleObject(*item, INFINITE) == WAIT_OBJECT_0, "waiting for probe event failed"); }
static int event_ready(event *item) { return WaitForSingleObject(*item, 0) == WAIT_OBJECT_0; }
static void event_close(event *item) { check(CloseHandle(*item) != 0, "closing probe event failed"); }
static thread thread_start(thread_entry entry, void *arg) {
    thread result = CreateThread(NULL, 0, entry, arg, 0, NULL);
    check(result != NULL, "creating probe thread failed");
    return result;
}
static void thread_join(thread item) {
    check(WaitForSingleObject(item, INFINITE) == WAIT_OBJECT_0, "joining probe thread failed");
    check(CloseHandle(item) != 0, "closing probe thread failed");
}
#else
typedef struct { pthread_mutex_t mu; pthread_cond_t changed; int signaled; } event;
typedef pthread_t thread;
#define THREAD_RESULT void *
#define THREAD_RETURN NULL
typedef void *(*thread_entry)(void *);
static void event_init(event *item) {
    check(pthread_mutex_init(&item->mu, NULL) == 0 && pthread_cond_init(&item->changed, NULL) == 0,
          "creating probe event failed");
    item->signaled = 0;
}
static void event_signal(event *item) {
    check(pthread_mutex_lock(&item->mu) == 0, "locking probe event failed");
    item->signaled = 1;
    check(pthread_cond_broadcast(&item->changed) == 0 && pthread_mutex_unlock(&item->mu) == 0,
          "signaling probe event failed");
}
static void event_wait(event *item) {
    check(pthread_mutex_lock(&item->mu) == 0, "locking probe event failed");
    while (!item->signaled) { check(pthread_cond_wait(&item->changed, &item->mu) == 0, "waiting for probe event failed"); }
    check(pthread_mutex_unlock(&item->mu) == 0, "unlocking probe event failed");
}
static int event_ready(event *item) {
    check(pthread_mutex_lock(&item->mu) == 0, "locking probe event failed");
    int ready = item->signaled;
    check(pthread_mutex_unlock(&item->mu) == 0, "unlocking probe event failed");
    return ready;
}
static void event_close(event *item) {
    check(pthread_cond_destroy(&item->changed) == 0 && pthread_mutex_destroy(&item->mu) == 0,
          "closing probe event failed");
}
static thread thread_start(thread_entry entry, void *arg) {
    thread result;
    check(pthread_create(&result, NULL, entry, arg) == 0, "creating probe thread failed");
    return result;
}
static void thread_join(thread item) { check(pthread_join(item, NULL) == 0, "joining probe thread failed"); }
#endif

static event callback_entered, callback_continue, shutdown_started, shutdown_done;

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
static unsigned callback_releases;
static int host_context;
static unsigned callback_mode;
static void *callback_buffer;
static size_t callback_length;
static int callback_outstanding;

static int login_host_call(void *ctx, const char *method, const uint8_t *request,
                           size_t len, buffer *response) {
    check(ctx == &host_context, "host context was not retained by value");
    check(strcmp(method, "host.http.do") == 0, "login bypassed host HTTP transport");
    buffer raw = {(void *)request, len};
    document doc = parse(raw);
    check(equals(&doc, field(&doc, 0, "host_callback_id"), "native-callback-id"),
          "host callback ID was not forwarded");
    check(equals(&doc, field(&doc, 0, "method"), "POST") &&
          equals(&doc, field(&doc, 0, "url"),
                 "https://copilot.tencent.com/v2/plugin/auth/state?platform=workbuddy"),
          "login state request used an incorrect endpoint");
    check(equals(&doc, field(&doc, 0, "body"), "e30="), "login state body must encode an empty object");
    int headers = field(&doc, 0, "headers");
    check(field(&doc, headers, "Authorization") == -1, "state request must not contain credentials");
    check(!callback_outstanding, "host buffer was not released before another callback");
    callback_calls++;
    if (callback_mode == 8) {
        event_signal(&callback_entered);
        event_wait(&callback_continue);
    }
    const char *reply = "{\"ok\":true,\"result\":{\"StatusCode\":200,\"Headers\":{},\"Body\":\"eyJjb2RlIjowLCJkYXRhIjp7InN0YXRlIjoibmF0aXZlLXByaXZhdGUtc3RhdGUiLCJhdXRoVXJsIjoiaHR0cHM6Ly93d3cud29ya2J1ZGR5LmNuL2xvZ2luP3BsYXRmb3JtPXdvcmtidWRkeSZzdGF0ZT1uYXRpdmUtcHJpdmF0ZS1zdGF0ZSJ9fQ==\"}}";
    if (callback_mode == 1) { reply = "transport-owned-buffer"; }
    if (callback_mode == 2) { reply = "["; }
    if (callback_mode == 7) { reply = "{\"ok\":true,\"result\":{\"StatusCode\":200,\"Body\":\"!\"}}"; }
    callback_length = strlen(reply);
    callback_buffer = malloc(callback_length);
    check(callback_buffer != NULL, "allocating host response failed");
    memcpy(callback_buffer, reply, callback_length);
    if (callback_mode == 3) { callback_length = SIZE_MAX; }
    if (callback_mode == 4) { callback_length = (size_t)2147483648ULL; }
    if (callback_mode == 5 || callback_mode == 6) {
        free(callback_buffer);
        callback_buffer = NULL;
        callback_length = callback_mode == 5 ? 1 : 0;
    }
    response->ptr = callback_buffer;
    response->len = callback_length;
    callback_outstanding = 1;
    return callback_mode == 1 ? -1 : 0;
}

static void login_host_free(void *ptr, size_t len) {
    check(callback_outstanding && ptr == callback_buffer && len == callback_length,
          "host release must receive its exact buffer and length once");
    callback_releases++;
    callback_outstanding = 0;
    free(ptr);
    callback_buffer = NULL;
}

static const char *login_request =
    "{\"Provider\":\"workbuddy\",\"Metadata\":{\"site\":\"workbuddy\"},\"host_callback_id\":\"native-callback-id\"}";

static void expect_failure(buffer response) {
    document doc = parse(response);
    check(equals(&doc, field(&doc, 0, "ok"), "false"), "callback failure must fail login");
    int error = field(&doc, 0, "error");
    int code = field(&doc, error, "code");
    int message = field(&doc, error, "message");
    check(code >= 0 && doc.tokens[code].kind == '"' && doc.tokens[code].len != 0 &&
          message >= 0 && doc.tokens[message].kind == '"' && doc.tokens[message].len != 0,
          "callback failure must retain a diagnostic error");
}

static void failed_login(plugin_api *api) {
    buffer response = {NULL, 0};
    check(api->call("auth.login.start", (const uint8_t *)login_request, strlen(login_request), &response) == 0,
          "login RPC transport failed");
    expect_failure(response);
    api->free_buffer(response.ptr, response.len);
}

static THREAD_RESULT login_during_shutdown(void *raw) {
    failed_login((plugin_api *)raw);
    return THREAD_RETURN;
}

static THREAD_RESULT shutdown_during_login(void *raw) {
    plugin_api *api = (plugin_api *)raw;
    event_signal(&shutdown_started);
    api->shutdown();
    event_signal(&shutdown_done);
    return THREAD_RETURN;
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
    host_api host = {1, &host_context, login_host_call, login_host_free};
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
    /* A real host may discard this stack table immediately after init. */
    host.host_ctx = NULL;
    host.call = NULL;
    host.free_buffer = NULL;
    const char *malformed = "{not-json}";
    failed_call(&api, "malformed-json", "plugin.register", (const uint8_t *)malformed, strlen(malformed), 0, "invalid_request");
    const uint8_t embedded_nul[] = {'{', '}', 0, '{', '}'};
    failed_call(&api, "embedded-nul", "plugin.register", embedded_nul, sizeof(embedded_nul), 0, "invalid_request");
    const char *schema = "{\"schema_version\":7}";
    failed_call(&api, "schema-mismatch", "plugin.register", (const uint8_t *)schema, strlen(schema), 0, "incompatible_schema");
    const char *empty = "{}";
    failed_call(&api, "unknown-method", "workbuddy.unknown", (const uint8_t *)empty, 2, 0, "unknown_method");
    failed_call(&api, "no-plugin-identifier", "plugin.identifier", (const uint8_t *)empty, 2, 0, "unknown_method");
    failed_call(&api, "auth-before-registration", "auth.identifier", (const uint8_t *)empty, 2, 0, "not_registered");
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
    check(object_size(&registered, capabilities) == 2 &&
          equals(&registered, field(&registered, capabilities, "management_api"), "true") &&
          equals(&registered, field(&registered, capabilities, "auth_provider"), "true"),
          "registration must advertise management and auth provider capabilities");
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

    buffer identifier = call(&api, "auth.identifier", "{}");
    document identity_doc = parse(identifier);
    int identity_result = field(&identity_doc, 0, "result");
    check(equals(&identity_doc, field(&identity_doc, identity_result, "identifier"), "workbuddy"),
          "incorrect auth provider identity");
    emit("auth-identifier", identifier);
    api.free_buffer(identifier.ptr, identifier.len);

    buffer login = call(&api, "auth.login.start", login_request);
    document login_doc = parse(login);
    int login_result = field(&login_doc, 0, "result");
    int login_state = field(&login_doc, login_result, "State");
    int login_url = field(&login_doc, login_result, "URL");
    check(equals(&login_doc, field(&login_doc, login_result, "Provider"), "workbuddy") &&
          login_state >= 0 && login_doc.tokens[login_state].kind == '"' && login_doc.tokens[login_state].len != 0 &&
          login_url >= 0 && login_doc.tokens[login_url].kind == '"' && login_doc.tokens[login_url].len != 0,
          "login start must return a provider, browser URL, and opaque state");
    api.free_buffer(login.ptr, login.len);
    check(callback_calls == 1 && callback_releases == 1 && !callback_outstanding,
          "successful login did not release its host result");
    for (callback_mode = 1; callback_mode <= 7; callback_mode++) {
        failed_login(&api);
        check(callback_calls == callback_mode + 1 && callback_releases == callback_calls && !callback_outstanding,
              "callback error did not release its host result");
    }
    /* Only counts are emitted; browser state and credentials stay private. */
    check(puts("host-roundtrip\t{\"ok\":true,\"result\":{\"callbacks\":8,\"releases\":8}}") >= 0,
          "writing callback summary failed");

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
    failed_login(&api);
    check(callback_calls == 8 && callback_releases == 8, "callbacks invoked after shutdown");
    /* Also exercise native shutdown without a preceding RPC shutdown and a
     * host with no callbacks: neither management nor identity requires them. */
    host.call = NULL;
    host.free_buffer = NULL;
    check(init(&host, &api) == 0, "reinitializing after shutdown failed");
    registration = call(&api, "plugin.register", "{\"schema_version\":6}");
    failed_login(&api);
    api.shutdown();
    failed_call(&api, "direct-native-shutdown", "management.handle", NULL, 0, 0, "plugin_shutdown");
    expect_ok(registration);
    api.free_buffer(registration.ptr, registration.len);
    check(callback_calls == 8 && callback_releases == 8 && !callback_outstanding,
          "read-only registration must work without callbacks");
    /* A call pointer without its release pair must also remain unavailable. */
    host.host_ctx = &host_context;
    host.call = login_host_call;
    check(init(&host, &api) == 0, "initializing without host release failed");
    registration = call(&api, "plugin.register", "{\"schema_version\":6}");
    api.free_buffer(registration.ptr, registration.len);
    failed_login(&api);
    api.shutdown();
    host.call = NULL;
    host.free_buffer = login_host_free;
    check(init(&host, &api) == 0, "initializing without host call failed");
    registration = call(&api, "plugin.register", "{\"schema_version\":6}");
    api.free_buffer(registration.ptr, registration.len);
    failed_login(&api);
    api.shutdown();
    check(callback_calls == 8 && callback_releases == 8, "incomplete callbacks were invoked");

    host.call = login_host_call;
    callback_mode = 8;
    check(init(&host, &api) == 0, "initializing concurrent shutdown probe failed");
    registration = call(&api, "plugin.register", "{\"schema_version\":6}");
    api.free_buffer(registration.ptr, registration.len);
    event_init(&callback_entered);
    event_init(&callback_continue);
    event_init(&shutdown_started);
    event_init(&shutdown_done);
    thread login_thread = thread_start(login_during_shutdown, &api);
    event_wait(&callback_entered);
    thread shutdown_thread = thread_start(shutdown_during_login, &api);
    event_wait(&shutdown_started);
    /* Observing closed admission proves native shutdown reached Runtime even
     * while the earlier call is still blocked inside its C host callback. */
    for (;;) {
        buffer response = {NULL, 0};
        check(api.call("auth.identifier", (const uint8_t *)"{}", 2, &response) == 0,
              "shutdown observation transport failed");
        document doc = parse(response);
        int closed = equals(&doc, field(&doc, 0, "ok"), "false");
        if (closed) { expect_error(response, "plugin_shutdown"); }
        api.free_buffer(response.ptr, response.len);
        if (closed) { break; }
    }
    check(!event_ready(&shutdown_done), "shutdown returned while a host callback was still active");
    event_signal(&callback_continue);
    thread_join(login_thread);
    thread_join(shutdown_thread);
    event_close(&callback_entered);
    event_close(&callback_continue);
    event_close(&shutdown_started);
    event_close(&shutdown_done);
    failed_login(&api);
    check(callback_calls == 9 && callback_releases == 9 && !callback_outstanding,
          "concurrent shutdown leaked or reused callbacks");
    check(puts("host-shutdown\t{\"ok\":true,\"result\":{\"callbacks\":9,\"releases\":9}}") >= 0,
          "writing shutdown summary failed");
#ifdef _WIN32
    check(FreeLibrary(library) != 0, "FreeLibrary failed");
#else
    check(dlclose(library) == 0, "dlclose failed");
#endif
    check(fflush(stdout) == 0, "flushing probe output failed");
    return 0;
}
