//go:build pipewire && cgo && linux

#ifndef WAYDICT_PIPEWIRE_CAPTURE_H
#define WAYDICT_PIPEWIRE_CAPTURE_H

#include <stdint.h>

typedef struct sv_pw_capture sv_pw_capture;

typedef struct {
  const char *target_object;
  uint32_t sample_rate;
  uint32_t channels;
  uint32_t ring_frames;
  uint32_t quantum_frames;
} sv_pw_config;

typedef struct {
  uint32_t sample_rate;
  float level_dbfs;
  uint64_t overruns;
  int capturing;
} sv_pw_stats;

int sv_pw_capture_new(const sv_pw_config *config, sv_pw_capture **out);
int sv_pw_capture_start(sv_pw_capture *capture, int timeout_ms);
int sv_pw_capture_pause(sv_pw_capture *capture);
int sv_pw_capture_stop(sv_pw_capture *capture);
int sv_pw_capture_read(sv_pw_capture *capture, float *dst, uint32_t max_frames, int timeout_ms);
int sv_pw_capture_stats(sv_pw_capture *capture, sv_pw_stats *out);
void sv_pw_capture_free(sv_pw_capture *capture);

#endif
