//go:build coreaudio && cgo && darwin

#include "ring.h"

#include <limits.h>
#include <stdlib.h>
#include <string.h>

bool wd_ca_checked_add(size_t a, size_t b, size_t *out) {
  if (out == NULL || a > SIZE_MAX - b) return false;
  *out = a + b;
  return true;
}

bool wd_ca_checked_mul(size_t a, size_t b, size_t *out) {
  if (out == NULL || (a != 0 && b > SIZE_MAX / a)) return false;
  *out = a * b;
  return true;
}

static bool align_up(size_t value, size_t alignment, size_t *out) {
  size_t extra = alignment - 1;
  size_t sum;
  if (!wd_ca_checked_add(value, extra, &sum)) return false;
  *out = sum & ~extra;
  return true;
}

bool wd_ca_raw_ring_init(wd_ca_raw_ring *ring,
                         size_t capacity,
                         uint32_t buffer_count,
                         const uint32_t *bytes_per_frame,
                         const uint32_t *channels_per_buffer,
                         uint32_t max_frames,
                         size_t max_slot_bytes,
                         size_t max_total_bytes) {
  if (ring == NULL || capacity == 0 || buffer_count == 0 ||
      buffer_count > WD_CA_MAX_BUFFERS || bytes_per_frame == NULL ||
      channels_per_buffer == NULL || max_frames == 0) {
    return false;
  }

  memset(ring, 0, sizeof(*ring));
  size_t payload = 0;
  for (uint32_t i = 0; i < buffer_count; i++) {
    if (bytes_per_frame[i] == 0 || channels_per_buffer[i] == 0) return false;
    size_t bytes;
    if (!wd_ca_checked_mul(bytes_per_frame[i], max_frames, &bytes)) return false;
    ring->payload_offsets[i] = payload;
    if (!wd_ca_checked_add(payload, bytes, &payload)) return false;
    ring->bytes_per_frame[i] = bytes_per_frame[i];
    ring->channels_per_buffer[i] = channels_per_buffer[i];
  }

  size_t stride;
  if (!wd_ca_checked_add(offsetof(wd_ca_raw_slot, payload), payload, &stride) ||
      !align_up(stride, _Alignof(max_align_t), &stride) ||
      stride > max_slot_bytes) {
    return false;
  }
  size_t total;
  if (!wd_ca_checked_mul(capacity, stride, &total) || total > max_total_bytes) return false;

  ring->storage = calloc(1, total);
  if (ring->storage == NULL) return false;
  ring->capacity = capacity;
  ring->stride = stride;
  ring->buffer_count = buffer_count;
  ring->payload_bytes = payload;
  atomic_init(&ring->read_index, 0);
  atomic_init(&ring->write_index, 0);
  return true;
}

void wd_ca_raw_ring_destroy(wd_ca_raw_ring *ring) {
  if (ring == NULL) return;
  free(ring->storage);
  memset(ring, 0, sizeof(*ring));
}

void wd_ca_raw_ring_clear(wd_ca_raw_ring *ring) {
  if (ring == NULL) return;
  atomic_store_explicit(&ring->read_index, 0, memory_order_relaxed);
  atomic_store_explicit(&ring->write_index, 0, memory_order_relaxed);
}

bool wd_ca_raw_ring_reserve(wd_ca_raw_ring *ring, wd_ca_raw_slot **slot, uint64_t *sequence) {
  uint64_t write = atomic_load_explicit(&ring->write_index, memory_order_relaxed);
  uint64_t read = atomic_load_explicit(&ring->read_index, memory_order_acquire);
  if (write - read >= ring->capacity) return false;
  *slot = (wd_ca_raw_slot *)(ring->storage + (write % ring->capacity) * ring->stride);
  *sequence = write;
  return true;
}

void wd_ca_raw_ring_publish(wd_ca_raw_ring *ring, uint64_t sequence) {
  atomic_store_explicit(&ring->write_index, sequence + 1, memory_order_release);
}

bool wd_ca_raw_ring_peek(wd_ca_raw_ring *ring, wd_ca_raw_slot **slot, uint64_t *sequence) {
  uint64_t read = atomic_load_explicit(&ring->read_index, memory_order_relaxed);
  uint64_t write = atomic_load_explicit(&ring->write_index, memory_order_acquire);
  if (read == write) return false;
  *slot = (wd_ca_raw_slot *)(ring->storage + (read % ring->capacity) * ring->stride);
  *sequence = read;
  return true;
}

void wd_ca_raw_ring_release(wd_ca_raw_ring *ring, uint64_t sequence) {
  atomic_store_explicit(&ring->read_index, sequence + 1, memory_order_release);
}

uint8_t *wd_ca_raw_slot_buffer(const wd_ca_raw_ring *ring, wd_ca_raw_slot *slot, uint32_t index) {
  if (ring == NULL || slot == NULL || index >= ring->buffer_count) return NULL;
  return slot->payload + ring->payload_offsets[index];
}

bool wd_ca_float_ring_init(wd_ca_float_ring *ring, size_t capacity) {
  if (ring == NULL || capacity == 0) return false;
  memset(ring, 0, sizeof(*ring));
  size_t bytes;
  if (!wd_ca_checked_mul(capacity, sizeof(float), &bytes)) return false;
  ring->samples = calloc(1, bytes);
  if (ring->samples == NULL) return false;
  ring->capacity = capacity;
  atomic_init(&ring->read_index, 0);
  atomic_init(&ring->write_index, 0);
  return true;
}

void wd_ca_float_ring_destroy(wd_ca_float_ring *ring) {
  if (ring == NULL) return;
  free(ring->samples);
  memset(ring, 0, sizeof(*ring));
}

void wd_ca_float_ring_clear(wd_ca_float_ring *ring) {
  if (ring == NULL) return;
  atomic_store_explicit(&ring->read_index, 0, memory_order_relaxed);
  atomic_store_explicit(&ring->write_index, 0, memory_order_relaxed);
}

size_t wd_ca_float_ring_available(const wd_ca_float_ring *ring) {
  if (ring == NULL) return 0;
  uint64_t read = atomic_load_explicit(&ring->read_index, memory_order_relaxed);
  uint64_t write = atomic_load_explicit(&ring->write_index, memory_order_acquire);
  uint64_t available = write - read;
  return available > SIZE_MAX ? SIZE_MAX : (size_t)available;
}

bool wd_ca_float_ring_write(wd_ca_float_ring *ring, const float *samples, size_t count) {
  if (ring == NULL || samples == NULL || count == 0 || count > ring->capacity) return count == 0;
  uint64_t write = atomic_load_explicit(&ring->write_index, memory_order_relaxed);
  uint64_t read = atomic_load_explicit(&ring->read_index, memory_order_acquire);
  if (count > ring->capacity - (size_t)(write - read)) return false;
  size_t offset = (size_t)(write % ring->capacity);
  size_t first = count < ring->capacity - offset ? count : ring->capacity - offset;
  memcpy(ring->samples + offset, samples, first * sizeof(float));
  memcpy(ring->samples, samples + first, (count - first) * sizeof(float));
  atomic_store_explicit(&ring->write_index, write + count, memory_order_release);
  return true;
}

size_t wd_ca_float_ring_read(wd_ca_float_ring *ring, float *samples, size_t count) {
  if (ring == NULL || samples == NULL || count == 0) return 0;
  uint64_t read = atomic_load_explicit(&ring->read_index, memory_order_relaxed);
  uint64_t write = atomic_load_explicit(&ring->write_index, memory_order_acquire);
  size_t available = (size_t)(write - read);
  if (count > available) count = available;
  size_t offset = (size_t)(read % ring->capacity);
  size_t first = count < ring->capacity - offset ? count : ring->capacity - offset;
  memcpy(samples, ring->samples + offset, first * sizeof(float));
  memcpy(samples + first, ring->samples, (count - first) * sizeof(float));
  atomic_store_explicit(&ring->read_index, read + count, memory_order_release);
  return count;
}

int wd_ca_ring_self_test(void) {
  wd_ca_float_ring ring;
  if (!wd_ca_float_ring_init(&ring, 4)) return 1;
  float first[] = {1.0f, 2.0f, 3.0f};
  float out[4] = {0};
  if (!wd_ca_float_ring_write(&ring, first, 3) ||
      wd_ca_float_ring_read(&ring, out, 2) != 2 || out[0] != 1.0f || out[1] != 2.0f) {
    wd_ca_float_ring_destroy(&ring);
    return 2;
  }
  float second[] = {4.0f, 5.0f, 6.0f};
  if (!wd_ca_float_ring_write(&ring, second, 3) || wd_ca_float_ring_write(&ring, first, 1) ||
      wd_ca_float_ring_read(&ring, out, 4) != 4 || out[0] != 3.0f || out[3] != 6.0f) {
    wd_ca_float_ring_destroy(&ring);
    return 3;
  }
  wd_ca_float_ring_destroy(&ring);

  wd_ca_raw_ring raw;
  uint32_t bytes_per_frame[] = {sizeof(float)};
  uint32_t channels[] = {1};
  if (!wd_ca_raw_ring_init(&raw, 2, 1, bytes_per_frame, channels, 4, 1024, 4096)) return 4;
  wd_ca_raw_slot *write_slot = NULL;
  uint64_t write_sequence = 0;
  if (!wd_ca_raw_ring_reserve(&raw, &write_slot, &write_sequence)) {
    wd_ca_raw_ring_destroy(&raw);
    return 5;
  }
  float value = 0.25f;
  write_slot->frame_count = 1;
  write_slot->buffer_count = 1;
  write_slot->byte_counts[0] = sizeof(value);
  memcpy(wd_ca_raw_slot_buffer(&raw, write_slot, 0), &value, sizeof(value));
  wd_ca_raw_ring_publish(&raw, write_sequence);
  wd_ca_raw_slot *read_slot = NULL;
  uint64_t read_sequence = 0;
  float copied = 0;
  if (!wd_ca_raw_ring_peek(&raw, &read_slot, &read_sequence) || read_slot->frame_count != 1) {
    wd_ca_raw_ring_destroy(&raw);
    return 6;
  }
  memcpy(&copied, wd_ca_raw_slot_buffer(&raw, read_slot, 0), sizeof(copied));
  wd_ca_raw_ring_release(&raw, read_sequence);
  wd_ca_raw_ring_destroy(&raw);
  if (copied != value) return 7;
  return 0;
}
