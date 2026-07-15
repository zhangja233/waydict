//go:build darwin && cgo

#ifndef WAYDICT_USER_DEFAULTS_STORE_H
#define WAYDICT_USER_DEFAULTS_STORE_H

#include <stdbool.h>

int waydict_defaults_copy_string(const char *key, char **value, bool *found);
int waydict_defaults_set_string(const char *key, const char *value);
int waydict_defaults_delete(const char *key);
void waydict_defaults_free(void *value);

#endif
