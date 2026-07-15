//go:build coreaudio && cgo && darwin

#ifndef WAYDICT_COREAUDIO_RING_H
#define WAYDICT_COREAUDIO_RING_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdatomic.h>

#define WD_CA_MAX_BUFFERS 32

typedef struct {
  uint32_t frame_count;
  uint32_t buffer_count;
  uint32_t byte_counts[WD_CA_MAX_BUFFERS];
  uint8_t payload[];
} wd_ca_raw_slot;

typedef struct {
  uint8_t *storage;
  size_t capacity;
  size_t stride;
  uint32_t buffer_count;
  uint32_t bytes_per_frame[WD_CA_MAX_BUFFERS];
  uint32_t channels_per_buffer[WD_CA_MAX_BUFFERS];
  size_t payload_offsets[WD_CA_MAX_BUFFERS];
  size_t payload_bytes;
  _Atomic uint64_t read_index;
  _Atomic uint64_t write_index;
} wd_ca_raw_ring;

typedef struct {
  float *samples;
  size_t capacity;
  _Atomic uint64_t read_index;
  _Atomic uint64_t write_index;
} wd_ca_float_ring;

bool wd_ca_checked_add(size_t a, size_t b, size_t *out);
bool wd_ca_checked_mul(size_t a, size_t b, size_t *out);

bool wd_ca_raw_ring_init(wd_ca_raw_ring *ring,
                         size_t capacity,
                         uint32_t buffer_count,
                         const uint32_t *bytes_per_frame,
                         const uint32_t *channels_per_buffer,
                         uint32_t max_frames,
                         size_t max_slot_bytes,
                         size_t max_total_bytes);
void wd_ca_raw_ring_destroy(wd_ca_raw_ring *ring);
void wd_ca_raw_ring_clear(wd_ca_raw_ring *ring);
bool wd_ca_raw_ring_reserve(wd_ca_raw_ring *ring, wd_ca_raw_slot **slot, uint64_t *sequence);
void wd_ca_raw_ring_publish(wd_ca_raw_ring *ring, uint64_t sequence);
bool wd_ca_raw_ring_peek(wd_ca_raw_ring *ring, wd_ca_raw_slot **slot, uint64_t *sequence);
void wd_ca_raw_ring_release(wd_ca_raw_ring *ring, uint64_t sequence);
uint8_t *wd_ca_raw_slot_buffer(const wd_ca_raw_ring *ring, wd_ca_raw_slot *slot, uint32_t index);

bool wd_ca_float_ring_init(wd_ca_float_ring *ring, size_t capacity);
void wd_ca_float_ring_destroy(wd_ca_float_ring *ring);
void wd_ca_float_ring_clear(wd_ca_float_ring *ring);
size_t wd_ca_float_ring_available(const wd_ca_float_ring *ring);
bool wd_ca_float_ring_write(wd_ca_float_ring *ring, const float *samples, size_t count);
size_t wd_ca_float_ring_read(wd_ca_float_ring *ring, float *samples, size_t count);

int wd_ca_ring_self_test(void);

#endif
