#include "native.h"
#include "_cgo_export.h"

static int workbuddy_call(const char *method, const uint8_t *request,
                         size_t request_len, cliproxy_buffer *response) {
    return workbuddyPluginCall((char *)method, (uint8_t *)request, request_len, response);
}

void workbuddy_set_api(cliproxy_plugin_api *api) {
    api->call = workbuddy_call;
    api->free_buffer = workbuddyPluginFree;
    api->shutdown = workbuddyPluginShutdown;
}
