#define _POSIX_C_SOURCE 200809L
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wunused-function"
#include "cpa-cursor.h"
#pragma GCC diagnostic pop

static void require(int condition, const char *message) {
    if (!condition) {
        fprintf(stderr, "ABI smoke: %s\n", message);
        exit(EXIT_FAILURE);
    }
}

static int host_call(void *ctx, const char *method, const uint8_t *request,
                     size_t length, cliproxy_buffer *response) {
    (void)ctx;
    require(method != NULL && response != NULL, "host callback arguments");
    const char *payload = NULL;
    if (strcmp(method,"host.http.operation_open") == 0) {
        payload = "{\"ok\":true,\"result\":{\"operation_id\":\"fixture-operation\"}}";
    } else if (strcmp(method,"host.http.do") == 0) {
        char *json = malloc(length + 1);
        require(json != NULL,"allocate callback request");
        memcpy(json,request,length); json[length] = '\0';
        require(strstr(json,"GetCurrentPeriodUsage") != NULL,"quota upstream route");
        require(strstr(json,"fixture-access") != NULL,"quota bearer credential");
        require(strstr(json,"fixture-callback") != NULL,"quota callback scope");
        free(json);
        payload = "{\"ok\":true,\"result\":{\"StatusCode\":200,\"Body\":\"eyJwbGFuVXNhZ2UiOnsidG90YWxQZXJjZW50VXNlZCI6MjV9fQ==\"}}";
    } else {
        require(0,"unexpected host callback (real network disabled)");
    }
    size_t size = strlen(payload);
    response->ptr = malloc(size);
    require(response->ptr != NULL,"allocate host response");
    memcpy(response->ptr,payload,size); response->len = size;
    return 0;
}

static void host_free(void *ptr, size_t length) {
    (void)length;
    free(ptr);
}

static void call(cliproxy_plugin_api *api, const char *method, const char *request) {
    cliproxy_buffer response = {0};
    size_t length = strlen(request);
    char *method_copy = strdup(method);
    require(method_copy != NULL, "allocate method");
    int code = api->call(method_copy, (uint8_t *)request, length, &response);
    free(method_copy);
    require(response.ptr != NULL && response.len > 0, "plugin response buffer");
    printf("{\"method\":\"%s\",\"code\":%d,\"response\":", method, code);
    require(fwrite(response.ptr, 1, response.len, stdout) == response.len,
            "write plugin response");
    puts("}");
    api->free_buffer(response.ptr, response.len);
}

int main(int argc, char **argv) {
    require(argc == 2, "usage: host LIBRARY");
    void *library = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    require(library != NULL, dlerror());
    void *symbol = dlsym(library, "cliproxy_plugin_init");
    require(symbol != NULL, "init export missing");
    int (*init_plugin)(cliproxy_host_api *, cliproxy_plugin_api *);
    _Static_assert(sizeof(init_plugin) == sizeof(symbol), "POSIX function pointer size");
    memcpy(&init_plugin, &symbol, sizeof(init_plugin));
    cliproxy_host_api host = {1, NULL, host_call, host_free};
    cliproxy_plugin_api api = {0};
    require(init_plugin(&host, &api) == 0, "plugin init");
    require(api.abi_version == 1 && api.call && api.free_buffer && api.shutdown,
            "ABI version/function table");

    const char *disabled =
        "{\"schema_version\":6,\"config_yaml\":\"ZW5hYmxlZDogZmFsc2UK\"}";
    call(&api, "plugin.register", disabled);
    call(&api, "auth.identifier", "{}");
    call(&api, "quota.identifier", "{}");
    call(&api, "quota.describe", "{}");
    call(&api, "plugin.quiesce", "{}");
    call(&api, "plugin.reconfigure", disabled);
    call(&api, "quota.fetch", "{}");
    call(&api, "plugin.reconfigure", "{}");
    call(&api, "quota.fetch", "{\"provider\":\"cursor\",\"storage_json\":\"eyJ0eXBlIjoiY3Vyc29yIiwiaWQiOiJmaXh0dXJlIiwiYWNjZXNzX3Rva2VuIjoiZml4dHVyZS1hY2Nlc3MifQ==\",\"host_callback_id\":\"fixture-callback\"}");
    call(&api, "quota.reset", "{\"provider\":\"cursor\"}");
    call(&api, "plugin.shutdown", "{}");
    api.shutdown();
    api.shutdown();
    return EXIT_SUCCESS;
}
