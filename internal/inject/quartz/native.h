//go:build darwin && cgo

#ifndef WAYDICT_INJECT_QUARTZ_NATIVE_H
#define WAYDICT_INJECT_QUARTZ_NATIVE_H

#include <stddef.h>
#include <stdint.h>

typedef struct wd_quartz_transaction wd_quartz_transaction;

typedef enum {
    WD_QUARTZ_RESULT_OK = 0,
    WD_QUARTZ_RESULT_INVALID = 1,
    WD_QUARTZ_RESULT_NO_MEMORY = 2,
    WD_QUARTZ_RESULT_SOURCE_FAILED = 3,
    WD_QUARTZ_RESULT_EVENT_FAILED = 4,
} wd_quartz_result;

int wd_quartz_available(void);
int wd_quartz_transaction_create(wd_quartz_transaction **out);
void wd_quartz_transaction_destroy(wd_quartz_transaction *transaction);
int wd_quartz_post_unicode(wd_quartz_transaction *transaction, const uint16_t *text, size_t length);
int wd_quartz_post_key(wd_quartz_transaction *transaction, uint16_t keycode);
uint16_t wd_quartz_return_keycode(void);
uint16_t wd_quartz_tab_keycode(void);

#endif
