//go:build coreaudio && cgo && darwin

#ifndef WAYDICT_COREAUDIO_NATIVE_H
#define WAYDICT_COREAUDIO_NATIVE_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef struct wd_ca_capture wd_ca_capture;

typedef enum {
  WD_CA_STATUS_OK = 0,
  WD_CA_STATUS_INVALID = 1,
  WD_CA_STATUS_NO_MEMORY = 2,
  WD_CA_STATUS_PERMISSION = 3,
  WD_CA_STATUS_DEVICE_NOT_FOUND = 4,
  WD_CA_STATUS_DEVICE_DISCONNECTED = 5,
  WD_CA_STATUS_DEVICE_CHANGED = 6,
  WD_CA_STATUS_FORMAT_CHANGED = 7,
  WD_CA_STATUS_CONVERTER_FAILED = 8,
  WD_CA_STATUS_START_FAILED = 9,
  WD_CA_STATUS_START_TIMEOUT = 10,
  WD_CA_STATUS_STOPPED = 11,
  WD_CA_STATUS_BACKEND_UNAVAILABLE = 12,
  WD_CA_STATUS_TEARDOWN_TIMEOUT = 13
} wd_ca_status;

typedef enum {
  WD_CA_PERMISSION_UNKNOWN = 0,
  WD_CA_PERMISSION_NOT_DETERMINED = 1,
  WD_CA_PERMISSION_RESTRICTED = 2,
  WD_CA_PERMISSION_DENIED = 3,
  WD_CA_PERMISSION_GRANTED = 4
} wd_ca_permission;

typedef enum {
  WD_CA_EVENT_NONE = 0,
  WD_CA_EVENT_DEFAULT_CHANGED = 1,
  WD_CA_EVENT_DEVICE_REMOVED = 2,
  WD_CA_EVENT_DEVICE_ADDED = 3,
  WD_CA_EVENT_FORMAT_CHANGED = 4,
  WD_CA_EVENT_OVERRUN = 5
} wd_ca_event_kind;

typedef struct {
  const char *uid;
  const char *name;
  bool is_default;
  bool connected;
} wd_ca_device;

typedef struct {
  wd_ca_device *items;
  size_t count;
} wd_ca_device_list;

typedef struct {
  uint32_t sample_rate;
  double level_dbfs;
  uint64_t overruns;
  bool capturing;
  char device_uid[512];
  char device_name[512];
  double input_latency_seconds;
} wd_ca_stats;

typedef struct {
  wd_ca_event_kind kind;
  char device_uid[512];
  wd_ca_status status;
} wd_ca_event;

int wd_ca_capture_create(uint32_t sample_rate,
                         uint32_t ring_seconds,
                         wd_ca_capture **out,
                         char **error);
int wd_ca_capture_start(wd_ca_capture *capture,
                        const char *device_uid,
                        uint32_t quantum_ms,
                        uint32_t timeout_ms,
                        char **error);
int wd_ca_capture_pause(wd_ca_capture *capture, char **error);
int wd_ca_capture_stop(wd_ca_capture *capture, char **error);
int wd_ca_capture_read(wd_ca_capture *capture,
                       float *samples,
                       size_t capacity,
                       uint32_t timeout_ms,
                       size_t *count,
                       char **error);
int wd_ca_capture_stats(wd_ca_capture *capture, wd_ca_stats *stats, char **error);
int wd_ca_capture_next_event(wd_ca_capture *capture,
                             uint32_t timeout_ms,
                             wd_ca_event *event,
                             char **error);
void wd_ca_capture_destroy(wd_ca_capture *capture);

int wd_ca_copy_devices(wd_ca_device_list **out, char **error);
void wd_ca_device_list_destroy(wd_ca_device_list *list);
int wd_ca_permission_state(wd_ca_permission *permission, char **error);

int wd_ca_test_convert(const float *interleaved,
                       uint32_t frames,
                       uint32_t channels,
                       double sample_rate,
                       float **out,
                       uint32_t *out_frames,
                       char **error);
int wd_ca_test_tap_gate(void);
int wd_ca_test_startup_format_change(void);
int wd_ca_test_teardown_timeout(uint32_t *elapsed_ms);

void wd_ca_free(void *value);

#endif
