package main

/*
#cgo CFLAGS: -Wall -Wextra -Werror
#include "native.h"
*/
import "C"

import (
	"sync"
	"unsafe"

	"cpa-workbuddy/internal/plugin"
)

var (
	nativeMu    sync.RWMutex
	native      *plugin.Runtime
	initialized bool
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, api *C.cliproxy_plugin_api) C.int {
	if api == nil {
		return -1
	}
	*api = C.cliproxy_plugin_api{}
	if host == nil || uint32(host.abi_version) != plugin.ABIVersion {
		return -2
	}
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if initialized {
		return -3
	}
	// This capability uses no host callbacks, so their pointers may be nil and
	// the host table need not outlive this handshake.
	native = plugin.New()
	initialized = true
	api.abi_version = C.uint32_t(plugin.ABIVersion)
	C.workbuddy_set_api(api)
	return 0
}

//export workbuddyPluginCall
func workbuddyPluginCall(method *C.char, request *C.uint8_t, length C.size_t, response *C.cliproxy_buffer) C.int {
	if response == nil {
		return -1
	}
	response.ptr = nil
	response.len = 0
	if method == nil {
		return writeResponse(response, plugin.Failure("invalid_method", "method is required", 400), 1)
	}
	// CPA's loader copies C buffers using a signed C int length. Reject before
	// converting or reading memory, including SIZE_MAX and nil/nonzero pairs.
	if uint64(length) > uint64(1<<31-1) || (request == nil && length != 0) {
		return writeResponse(response, plugin.Failure("invalid_request", "invalid request pointer or length", 400), 1)
	}
	nativeMu.RLock()
	defer nativeMu.RUnlock()
	if native == nil {
		return writeResponse(response, plugin.Failure("not_initialized", "native initialization is required", 409), 1)
	}
	payload := unsafe.Slice((*byte)(unsafe.Pointer(request)), int(length))
	return writeResponse(response, native.Handle(C.GoString(method), payload), 0)
}

//export workbuddyPluginFree
func workbuddyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	// Each returned buffer is an independent C allocation; only this callback
	// releases it. The ABI's length is informational and free(NULL) is valid.
	_ = length
	C.free(ptr)
}

//export workbuddyPluginShutdown
func workbuddyPluginShutdown() {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if native != nil {
		native.Shutdown()
	}
	initialized = false
}

func writeResponse(response *C.cliproxy_buffer, raw []byte, code C.int) C.int {
	ptr := C.CBytes(raw)
	if ptr == nil {
		return -4
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
	return code
}
