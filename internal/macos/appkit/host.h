#ifndef WAYDICT_APPKIT_HOST_H
#define WAYDICT_APPKIT_HOST_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef void *waydict_host_t;

waydict_host_t waydict_host_create(void);
void waydict_host_destroy(waydict_host_t host);
int waydict_host_run(waydict_host_t host);
void waydict_host_terminate(waydict_host_t host);
void waydict_host_update_status(waydict_host_t host, const char *json, size_t length);
void waydict_host_show_error(waydict_host_t host, const char *code, const char *message);
void waydict_host_show_diagnostics(waydict_host_t host, const char *report, size_t length, bool copy_only);
char *waydict_host_copy_installation_json(waydict_host_t host);
void waydict_host_free_string(char *value);
bool waydict_host_open_path(waydict_host_t host, const char *path);
bool waydict_host_reveal_path(waydict_host_t host, const char *path);

bool waydict_activate_bundle(const char *bundle_id);
bool waydict_activate_pid(int32_t pid);
int32_t waydict_frontmost_pid(void);

#endif
