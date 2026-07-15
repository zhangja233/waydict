//go:build darwin && cgo

#ifndef WAYDICT_FOCUS_MACOS_NATIVE_H
#define WAYDICT_FOCUS_MACOS_NATIVE_H

#include <stdint.h>

typedef struct wd_focus_provider wd_focus_provider;

typedef enum {
    WD_FOCUS_RESULT_OK = 0,
    WD_FOCUS_RESULT_INVALID = 1,
    WD_FOCUS_RESULT_NO_MEMORY = 2,
    WD_FOCUS_RESULT_PERMISSION = 3,
    WD_FOCUS_RESULT_TRANSIENT = 4,
    WD_FOCUS_RESULT_UNAVAILABLE = 5,
} wd_focus_result;

typedef struct {
    uint64_t token;
    int32_t pid;
    int secure_field;
    char *stable_id;
    char *app_id;
    char *app_name;
    char *degraded_reason;
} wd_focus_target;

int wd_focus_provider_create(wd_focus_provider **out, char **error);
void wd_focus_provider_destroy(wd_focus_provider *provider);
int wd_focus_available(void);
int wd_focus_current(wd_focus_provider *provider, wd_focus_target *target, char **error);
int wd_focus_same(wd_focus_provider *provider, uint64_t token, wd_focus_target *current, int *same, char **error);
void wd_focus_release(wd_focus_provider *provider, uint64_t token);
void wd_focus_target_clear(wd_focus_target *target);
void wd_focus_free(void *value);

#endif
