//go:build pipewire && cgo && linux

#include "pipewire_capture.h"

#include <math.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include <pipewire/pipewire.h>
#include <spa/buffer/buffer.h>
#include <spa/param/audio/format-utils.h>
#include <spa/param/props.h>
#include <spa/pod/builder.h>
#include <spa/utils/defs.h>

struct sv_pw_capture {
  struct pw_thread_loop *loop;
  struct pw_context *context;
  struct pw_core *core;
  struct pw_stream *stream;
  struct spa_hook stream_listener;
  float *ring;
  uint32_t capacity;
  uint32_t sample_rate;
  uint32_t channels;
  atomic_uint_fast64_t read_count;
  atomic_uint_fast64_t write_count;
  atomic_uint_fast64_t overruns;
  atomic_int capturing;
  atomic_int level_mdb;
};

static void ring_write(struct sv_pw_capture *c, const float *src, uint32_t n) {
  for (uint32_t i = 0; i < n; i++) {
    uint64_t w = atomic_load_explicit(&c->write_count, memory_order_relaxed);
    uint64_t r = atomic_load_explicit(&c->read_count, memory_order_acquire);
    if (w - r >= c->capacity) {
      atomic_store_explicit(&c->read_count, w - c->capacity + 1, memory_order_release);
      atomic_fetch_add_explicit(&c->overruns, 1, memory_order_relaxed);
    }
    c->ring[w % c->capacity] = src[i];
    atomic_store_explicit(&c->write_count, w + 1, memory_order_release);
  }
}

static void update_level(struct sv_pw_capture *c, const float *src, uint32_t n) {
  if (n == 0) {
    atomic_store_explicit(&c->level_mdb, -120000, memory_order_relaxed);
    return;
  }
  double sum = 0.0;
  for (uint32_t i = 0; i < n; i++) {
    double v = src[i];
    sum += v * v;
  }
  double rms = sqrt(sum / (double)n);
  double db = rms > 0.0 ? 20.0 * log10(rms) : -120.0;
  if (db < -120.0) db = -120.0;
  atomic_store_explicit(&c->level_mdb, (int)(db * 1000.0), memory_order_relaxed);
}

static void on_process(void *data) {
  struct sv_pw_capture *c = data;
  struct pw_buffer *b = pw_stream_dequeue_buffer(c->stream);
  if (b == NULL) return;
  struct spa_buffer *buf = b->buffer;
  if (buf != NULL && buf->n_datas > 0) {
    struct spa_data *d = &buf->datas[0];
    if (d->data != NULL && d->chunk != NULL && d->chunk->size > 0) {
      uint8_t *bytes = SPA_MEMBER(d->data, d->chunk->offset, uint8_t);
      uint32_t frames = d->chunk->size / sizeof(float);
      ring_write(c, (const float *)bytes, frames);
      update_level(c, (const float *)bytes, frames);
    }
  }
  pw_stream_queue_buffer(c->stream, b);
}

static void on_state_changed(void *data, enum pw_stream_state old, enum pw_stream_state state, const char *error) {
  (void)old;
  (void)error;
  struct sv_pw_capture *c = data;
  atomic_store_explicit(&c->capturing, state == PW_STREAM_STATE_STREAMING, memory_order_relaxed);
}

static const struct pw_stream_events stream_events = {
  PW_VERSION_STREAM_EVENTS,
  .state_changed = on_state_changed,
  .process = on_process,
};

int sv_pw_capture_new(const sv_pw_config *config, sv_pw_capture **out) {
  if (config == NULL || out == NULL) return -1;
  *out = NULL;
  pw_init(NULL, NULL);
  struct sv_pw_capture *c = calloc(1, sizeof(*c));
  if (c == NULL) return -2;
  c->sample_rate = config->sample_rate ? config->sample_rate : 16000;
  c->channels = config->channels ? config->channels : 1;
  c->capacity = config->ring_frames ? config->ring_frames : c->sample_rate * 8;
  c->ring = calloc(c->capacity, sizeof(float));
  if (c->ring == NULL) {
    free(c);
    return -3;
  }
  atomic_store(&c->level_mdb, -120000);
  c->loop = pw_thread_loop_new("sway-voice-capture", NULL);
  if (c->loop == NULL) goto fail;
  pw_thread_loop_lock(c->loop);
  c->context = pw_context_new(pw_thread_loop_get_loop(c->loop), NULL, 0);
  if (c->context == NULL) goto fail_unlock;
  c->core = pw_context_connect(c->context, NULL, 0);
  if (c->core == NULL) goto fail_unlock;
  struct pw_properties *props = pw_properties_new(
    PW_KEY_MEDIA_TYPE, "Audio",
    PW_KEY_MEDIA_CATEGORY, "Capture",
    PW_KEY_MEDIA_ROLE, "Communication",
    NULL);
  if (config->target_object != NULL && config->target_object[0] != '\0') {
    pw_properties_set(props, PW_KEY_TARGET_OBJECT, config->target_object);
  }
  c->stream = pw_stream_new(c->core, "sway-voice-input", props);
  if (c->stream == NULL) goto fail_unlock;
  pw_stream_add_listener(c->stream, &c->stream_listener, &stream_events, c);

  uint8_t buffer[1024];
  struct spa_pod_builder b = SPA_POD_BUILDER_INIT(buffer, sizeof(buffer));
  const struct spa_pod *params[1];
  struct spa_audio_info_raw info = {
    .format = SPA_AUDIO_FORMAT_F32_LE,
    .rate = c->sample_rate,
    .channels = c->channels,
  };
  params[0] = spa_format_audio_raw_build(&b, SPA_PARAM_EnumFormat, &info);
  int rc = pw_stream_connect(c->stream,
                             PW_DIRECTION_INPUT,
                             PW_ID_ANY,
                             PW_STREAM_FLAG_AUTOCONNECT |
                               PW_STREAM_FLAG_MAP_BUFFERS |
                               PW_STREAM_FLAG_RT_PROCESS |
                               PW_STREAM_FLAG_INACTIVE,
                             params,
                             1);
  if (rc < 0) goto fail_unlock;
  if (pw_thread_loop_start(c->loop) < 0) goto fail_unlock;
  pw_thread_loop_unlock(c->loop);
  *out = c;
  return 0;

fail_unlock:
  pw_thread_loop_unlock(c->loop);
fail:
  sv_pw_capture_free(c);
  return -4;
}

int sv_pw_capture_start(sv_pw_capture *capture) {
  if (capture == NULL || capture->stream == NULL) return -1;
  pw_thread_loop_lock(capture->loop);
  int rc = pw_stream_set_active(capture->stream, true);
  pw_thread_loop_unlock(capture->loop);
  return rc;
}

int sv_pw_capture_pause(sv_pw_capture *capture) {
  if (capture == NULL || capture->stream == NULL) return -1;
  pw_thread_loop_lock(capture->loop);
  int rc = pw_stream_set_active(capture->stream, false);
  pw_thread_loop_unlock(capture->loop);
  atomic_store(&capture->capturing, 0);
  return rc;
}

int sv_pw_capture_stop(sv_pw_capture *capture) {
  return sv_pw_capture_pause(capture);
}

int sv_pw_capture_read(sv_pw_capture *capture, float *dst, uint32_t max_frames, int timeout_ms) {
  if (capture == NULL || dst == NULL) return -1;
  int waited = 0;
  for (;;) {
    uint64_t r = atomic_load_explicit(&capture->read_count, memory_order_relaxed);
    uint64_t w = atomic_load_explicit(&capture->write_count, memory_order_acquire);
    uint64_t avail = w - r;
    if (avail > 0) {
      uint32_t n = avail > max_frames ? max_frames : (uint32_t)avail;
      for (uint32_t i = 0; i < n; i++) {
        dst[i] = capture->ring[(r + i) % capture->capacity];
      }
      atomic_store_explicit(&capture->read_count, r + n, memory_order_release);
      return (int)n;
    }
    if (timeout_ms >= 0 && waited >= timeout_ms) return 0;
    struct timespec ts = {.tv_sec = 0, .tv_nsec = 5 * 1000 * 1000};
    nanosleep(&ts, NULL);
    waited += 5;
  }
}

int sv_pw_capture_stats(sv_pw_capture *capture, sv_pw_stats *out) {
  if (capture == NULL || out == NULL) return -1;
  out->sample_rate = capture->sample_rate;
  out->level_dbfs = (float)atomic_load(&capture->level_mdb) / 1000.0f;
  out->overruns = atomic_load(&capture->overruns);
  out->capturing = atomic_load(&capture->capturing);
  return 0;
}

void sv_pw_capture_free(sv_pw_capture *capture) {
  if (capture == NULL) return;
  if (capture->loop != NULL) {
    pw_thread_loop_lock(capture->loop);
    if (capture->stream != NULL) {
      pw_stream_destroy(capture->stream);
      capture->stream = NULL;
    }
    if (capture->core != NULL) {
      pw_core_disconnect(capture->core);
      capture->core = NULL;
    }
    if (capture->context != NULL) {
      pw_context_destroy(capture->context);
      capture->context = NULL;
    }
    pw_thread_loop_unlock(capture->loop);
    pw_thread_loop_stop(capture->loop);
    pw_thread_loop_destroy(capture->loop);
  }
  free(capture->ring);
  free(capture);
}
