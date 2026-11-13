package main

/*
#cgo CFLAGS: -Wall -Wextra -Werror
#include "native.h"
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"cpa-workbuddy/internal/plugin"
)

var (
	nativeLifecycleMu sync.Mutex
	nativeMu          sync.RWMutex
	native            *plugin.Runtime
	initialized       bool
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
	nativeLifecycleMu.Lock()
	defer nativeLifecycleMu.Unlock()
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if initialized {
		return -3
	}
	// The host owns its table, so retain the callbacks and opaque context by
	// value rather than retaining the handshake's table address.
	hostAPI := *host
	native = plugin.NewWithHost(func(method string, raw []byte) ([]byte, error) {
		return callHost(&hostAPI, method, raw)
	})
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
	if uint64(length) > uint64(plugin.MaxRPCPayload) {
		return writeResponse(response, plugin.Failure("request_too_large", "plugin RPC request exceeds the 64 MiB limit", 413), 1)
	}
	nativeMu.RLock()
	runtime := native
	nativeMu.RUnlock()
	if runtime == nil {
		return writeResponse(response, plugin.Failure("not_initialized", "native initialization is required", 409), 1)
	}
	payload := unsafe.Slice((*byte)(unsafe.Pointer(request)), int(length))
	return writeResponse(response, runtime.Handle(C.GoString(method), payload), 0)
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
	nativeLifecycleMu.Lock()
	defer nativeLifecycleMu.Unlock()
	nativeMu.RLock()
	runtime := native
	nativeMu.RUnlock()
	if runtime != nil {
		runtime.Shutdown()
	}
	nativeMu.Lock()
	initialized = false
	nativeMu.Unlock()
}

func callHost(host *C.cliproxy_host_api, method string, raw []byte) ([]byte, error) {
	if host.call == nil || host.free_buffer == nil {
		return nil, errors.New("host callbacks unavailable")
	}
	if len(raw) > plugin.MaxRPCPayload {
		return nil, errors.New("host callback request exceeds the 64 MiB limit")
	}
	name := C.CString(method)
	defer C.free(unsafe.Pointer(name))
	var request *C.uint8_t
	if len(raw) != 0 {
		request = (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(raw)))
	}
	var response C.cliproxy_buffer
	code := C.workbuddy_host_call(host, name, request, C.size_t(len(raw)), &response)
	// The callback transfers its buffer even when transport fails. Only the
	// host's matching release function may release that allocation.
	defer C.workbuddy_host_free(host, response.ptr, response.len)
	if code != 0 {
		return nil, errors.New("host callback transport failed")
	}
	if uint64(response.len) > uint64(1<<31-1) || response.ptr == nil && response.len != 0 {
		return nil, errors.New("invalid host callback response")
	}
	if uint64(response.len) > uint64(plugin.MaxRPCPayload) {
		return nil, errors.New("host callback response exceeds the 64 MiB limit")
	}
	return C.GoBytes(response.ptr, C.int(response.len)), nil
}

func writeResponse(response *C.cliproxy_buffer, raw []byte, code C.int) C.int {
	if len(raw) > plugin.MaxRPCPayload {
		raw = plugin.Failure("response_too_large", "plugin RPC response exceeds the 64 MiB limit", 502)
		code = 1
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return -4
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
	return code
}
