//go:build coreaudio && cgo && darwin

#import "native.h"
#import "ring.h"

#import <AVFoundation/AVFoundation.h>
#import <AudioToolbox/AudioToolbox.h>
#import <CoreAudio/CoreAudio.h>
#import <Foundation/Foundation.h>
#import <mach/mach.h>
#import <mach/semaphore.h>
#import <mach/task.h>

#include <errno.h>
#include <math.h>
#include <pthread.h>
#include <sched.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static const size_t WD_CA_MAX_SLOT_BYTES = 1024 * 1024;
static const size_t WD_CA_MAX_RAW_BYTES = 64 * 1024 * 1024;
static const size_t WD_CA_EVENT_CAPACITY = 64;
static const double WD_CA_LEVEL_FLOOR = -120.0;
static const double WD_CA_MIN_TAP_MS = 100.0;
static const double WD_CA_MAX_TAP_MS = 400.0;
static const uint32_t WD_CA_TEARDOWN_TIMEOUT_MS = 250;

typedef enum {
  WD_CA_STATE_IDLE = 0,
  WD_CA_STATE_STARTING = 1,
  WD_CA_STATE_CAPTURING = 2,
  WD_CA_STATE_PAUSING = 3,
  WD_CA_STATE_STOPPING = 4,
  WD_CA_STATE_STOPPED = 5
} wd_ca_state;

typedef enum {
  WD_CA_START_PENDING = 0,
  WD_CA_START_REARM = 1,
  WD_CA_START_CONFIRMED = 2
} wd_ca_start_phase;

typedef enum {
  WD_CA_TAP_FAILURE_NONE = 0,
  WD_CA_TAP_FAILURE_GENERATION = 1,
  WD_CA_TAP_FAILURE_FRAME_CAPACITY = 2,
  WD_CA_TAP_FAILURE_BUFFER_LIST = 3,
  WD_CA_TAP_FAILURE_BUFFER_COUNT = 4,
  WD_CA_TAP_FAILURE_BUFFER_LAYOUT = 5
} wd_ca_tap_failure;

@interface WDCATapContext : NSObject {
@public
  _Atomic(wd_ca_capture *) _capture;
  _Atomic bool _closing;
  _Atomic uint32_t _callbacks;
}
- (instancetype)initWithCapture:(wd_ca_capture *)capture;
@end

@implementation WDCATapContext
- (instancetype)initWithCapture:(wd_ca_capture *)capture {
  self = [super init];
  if (self != nil) {
    atomic_init(&_capture, capture);
    atomic_init(&_closing, false);
    atomic_init(&_callbacks, 0);
  }
  return self;
}
@end

@interface WDCASession : NSObject {
@public
  AVAudioEngine *_engine;
  AVAudioInputNode *_inputNode;
  AVAudioFormat *_nodeInputFormat;
  AVAudioFormat *_tapFormat;
  AVAudioFormat *_converterFormat;
  AVAudioConverter *_converter;
  AVAudioPCMBuffer *_inputScratch;
  AVAudioPCMBuffer *_outputScratch;
  AVAudioConverterInputBlock _inputBlock;
  WDCATapContext *_tapContext;
  BOOL _inputProvided;
  BOOL _tapInstalled;
}
@end

@implementation WDCASession
@end

struct wd_ca_capture {
  wd_ca_float_ring processed;
  wd_ca_raw_ring raw;
  semaphore_t raw_semaphore;
  _Atomic bool semaphore_created;
  pthread_t conversion_thread;
  bool conversion_thread_started;

  pthread_mutex_t wait_mutex;
  pthread_cond_t wait_cond;
  pthread_cond_t start_cond;
  pthread_mutex_t event_mutex;
  pthread_cond_t event_cond;
  pthread_mutex_t device_mutex;
  pthread_mutex_t lifecycle_mutex;
  pthread_cond_t lifecycle_cond;

  pthread_t teardown_thread;
  bool teardown_thread_started;
  bool teardown_requested;
  bool teardown_done;
  bool teardown_detached;
  bool destroy_requested;
  int teardown_result;
  char teardown_detail[256];

  wd_ca_event events[64];
  size_t event_head;
  size_t event_count;
  bool event_overflow;

  _Atomic int state;
  _Atomic int terminal_status;
  _Atomic bool conversion_exit;
  _Atomic int start_phase;
  _Atomic uint64_t generation;
  _Atomic uint64_t overruns;
  _Atomic uint64_t tap_callbacks;
  _Atomic uint64_t tap_frames_seen;
  _Atomic uint64_t raw_buffers;
  _Atomic uint64_t raw_frames;
  _Atomic uint64_t converted_buffers;
  _Atomic uint64_t converted_frames;
  _Atomic uint64_t format_change_events;
  _Atomic uint64_t ignored_startup_format_events;
  _Atomic uint32_t tap_max_frames;
  _Atomic uint32_t tap_last_buffer_count;
  _Atomic int tap_failure;
  _Atomic int level_mdb;
  _Atomic bool default_device_dirty;
  _Atomic bool stop_requested;

  uint32_t sample_rate;
  uint32_t quantum_ms;
  uint32_t tap_frames;
  uint32_t input_buffers;
  _Atomic uint32_t selected_device;
  AudioDeviceID listener_device;
  _Atomic bool explicit_device;
  bool default_listener_registered;
  bool device_listeners_registered;
  char *device_uid;
  char *device_name;
  double input_latency_seconds;
  void *session_ref;

  bool wait_mutex_initialized;
  bool wait_cond_initialized;
  bool start_cond_initialized;
  bool event_mutex_initialized;
  bool event_cond_initialized;
  bool device_mutex_initialized;
  bool lifecycle_mutex_initialized;
  bool lifecycle_cond_initialized;
};

typedef struct {
  AudioDeviceID id;
  char *uid;
  char *name;
  bool alive;
  bool input;
} wd_ca_device_info;

static OSStatus wd_ca_property_listener(AudioObjectID object,
                                        UInt32 address_count,
                                        const AudioObjectPropertyAddress addresses[],
                                        void *context);
static bool wd_ca_store_terminal(wd_ca_capture *capture, wd_ca_status status);
static void wd_ca_wake_readers(wd_ca_capture *capture);
static void wd_ca_finalize_destroy(wd_ca_capture *capture);

static void wd_ca_set_error(char **error, const char *format, ...) {
  if (error == NULL) return;
  *error = NULL;
  va_list args;
  va_start(args, format);
  (void)vasprintf(error, format, args);
  va_end(args);
}

static void wd_ca_set_nserror(char **error, NSString *operation, NSError *value) {
  const char *op = operation.UTF8String ?: "CoreAudio operation";
  const char *detail = value.localizedDescription.UTF8String ?: "unknown error";
  wd_ca_set_error(error, "%s: %s", op, detail);
}

static const char *wd_ca_tap_failure_name(int failure) {
  switch (failure) {
    case WD_CA_TAP_FAILURE_GENERATION: return "generation";
    case WD_CA_TAP_FAILURE_FRAME_CAPACITY: return "frame-capacity";
    case WD_CA_TAP_FAILURE_BUFFER_LIST: return "buffer-list";
    case WD_CA_TAP_FAILURE_BUFFER_COUNT: return "buffer-count";
    case WD_CA_TAP_FAILURE_BUFFER_LAYOUT: return "buffer-layout";
    default: return "none";
  }
}

static void wd_ca_describe_format(AVAudioFormat *format, char *out, size_t capacity) {
  if (out == NULL || capacity == 0) return;
  if (format == nil) {
    snprintf(out, capacity, "unavailable");
    return;
  }
  const AudioStreamBasicDescription *description = format.streamDescription;
  if (description == NULL) {
    snprintf(out, capacity, "unavailable");
    return;
  }
  snprintf(out,
           capacity,
           "sample_rate=%.3f,channel_count=%u,format_id=0x%08x,format_flags=0x%08x,bytes_per_frame=%u",
           format.sampleRate,
           format.channelCount,
           (unsigned int)description->mFormatID,
           (unsigned int)description->mFormatFlags,
           (unsigned int)description->mBytesPerFrame);
}

static void wd_ca_collect_diagnostics(wd_ca_capture *capture,
                                      WDCASession *session,
                                      char *out,
                                      size_t capacity) {
  if (out == NULL || capacity == 0) return;
  char uid[512] = {0};
  pthread_mutex_lock(&capture->device_mutex);
  if (capture->device_uid != NULL) strncpy(uid, capture->device_uid, sizeof(uid) - 1);
  pthread_mutex_unlock(&capture->device_mutex);

  AVAudioFormat *live_input = nil;
  AVAudioFormat *live_output = nil;
  if (session != nil) {
    @try {
      live_input = [session->_inputNode inputFormatForBus:0];
      live_output = [session->_inputNode outputFormatForBus:0];
    } @catch (__unused NSException *exception) {
    }
  }
  char setup_input[160];
  char setup_output[160];
  char current_input[160];
  char current_output[160];
  wd_ca_describe_format(session == nil ? nil : session->_nodeInputFormat,
                        setup_input,
                        sizeof(setup_input));
  wd_ca_describe_format(session == nil ? nil : session->_tapFormat,
                        setup_output,
                        sizeof(setup_output));
  wd_ca_describe_format(live_input, current_input, sizeof(current_input));
  wd_ca_describe_format(live_output, current_output, sizeof(current_output));

  snprintf(out,
           capacity,
           "device_uid=\"%s\" device_id=%u setup_input={%s} setup_output={%s} "
           "live_input={%s} live_output={%s} engine_running=%s tap_installed=%s "
           "quantum_ms=%u tap_capacity=%u tap_fired=%s tap_callbacks=%llu tap_frames_seen=%llu "
           "tap_max_frames=%u tap_last_buffer_count=%u raw_buffers=%llu raw_frames=%llu "
           "converted_buffers=%llu converted_frames=%llu format_change_events=%llu "
           "ignored_startup_format_events=%llu tap_failure=%s",
           uid[0] == '\0' ? "<unresolved>" : uid,
           atomic_load_explicit(&capture->selected_device, memory_order_acquire),
           setup_input,
           setup_output,
           current_input,
           current_output,
           session != nil && session->_engine.isRunning ? "true" : "false",
           session != nil && session->_tapInstalled ? "true" : "false",
           capture->quantum_ms,
           capture->tap_frames,
           atomic_load_explicit(&capture->tap_callbacks, memory_order_relaxed) != 0 ? "true" : "false",
           (unsigned long long)atomic_load_explicit(&capture->tap_callbacks, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->tap_frames_seen, memory_order_relaxed),
           atomic_load_explicit(&capture->tap_max_frames, memory_order_relaxed),
           atomic_load_explicit(&capture->tap_last_buffer_count, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->raw_buffers, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->raw_frames, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->converted_buffers, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->converted_frames, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->format_change_events, memory_order_relaxed),
           (unsigned long long)atomic_load_explicit(&capture->ignored_startup_format_events,
                                                    memory_order_relaxed),
           wd_ca_tap_failure_name(atomic_load_explicit(&capture->tap_failure, memory_order_relaxed)));
}

static void wd_ca_log_diagnostics(wd_ca_capture *capture,
                                  WDCASession *session,
                                  const char *phase,
                                  const char *branch,
                                  int status,
                                  uint32_t timeout_ms) {
  char diagnostics[2048];
  wd_ca_collect_diagnostics(capture, session, diagnostics, sizeof(diagnostics));
  fprintf(stderr,
          "level=INFO msg=\"CoreAudio capture diagnostic\" phase=%s branch=%s status=%d "
          "timeout_ms=%u %s\n",
          phase,
          branch,
          status,
          timeout_ms,
          diagnostics);
  fflush(stderr);
}

static void wd_ca_append_start_diagnostics(wd_ca_capture *capture,
                                           WDCASession *session,
                                           const char *branch,
                                           int status,
                                           uint32_t timeout_ms,
                                           char **error) {
  char diagnostics[2048];
  wd_ca_collect_diagnostics(capture, session, diagnostics, sizeof(diagnostics));
  fprintf(stderr,
          "level=INFO msg=\"CoreAudio capture diagnostic\" phase=start branch=%s status=%d "
          "timeout_ms=%u %s\n",
          branch,
          status,
          timeout_ms,
          diagnostics);
  fflush(stderr);
  if (error == NULL) return;
  char *cause = *error;
  *error = NULL;
  if (cause == NULL) {
    wd_ca_set_error(error,
                    "CoreAudio startup branch=%s status=%d; %s",
                    branch,
                    status,
                    diagnostics);
  } else {
    wd_ca_set_error(error,
                    "%s; CoreAudio diagnostics branch=%s status=%d %s",
                    cause,
                    branch,
                    status,
                    diagnostics);
  }
  free(cause);
}

void wd_ca_free(void *value) {
  free(value);
}

static struct timespec wd_ca_deadline(uint32_t timeout_ms) {
  struct timespec deadline;
  clock_gettime(CLOCK_REALTIME, &deadline);
  deadline.tv_sec += timeout_ms / 1000;
  deadline.tv_nsec += (long)(timeout_ms % 1000) * 1000000L;
  if (deadline.tv_nsec >= 1000000000L) {
    deadline.tv_sec++;
    deadline.tv_nsec -= 1000000000L;
  }
  return deadline;
}

static bool wd_ca_deadline_passed(const struct timespec *deadline) {
  struct timespec now;
  clock_gettime(CLOCK_REALTIME, &now);
  return now.tv_sec > deadline->tv_sec ||
         (now.tv_sec == deadline->tv_sec && now.tv_nsec >= deadline->tv_nsec);
}

static char *wd_ca_copy_cf_string_property(AudioObjectID object,
                                            AudioObjectPropertySelector selector,
                                            AudioObjectPropertyScope scope) {
  AudioObjectPropertyAddress address = {selector, scope, kAudioObjectPropertyElementMain};
  CFStringRef value = NULL;
  UInt32 size = sizeof(value);
  if (AudioObjectGetPropertyData(object, &address, 0, NULL, &size, &value) != noErr || value == NULL) {
    return NULL;
  }
  CFIndex length = CFStringGetLength(value);
  CFIndex maximum = CFStringGetMaximumSizeForEncoding(length, kCFStringEncodingUTF8);
  char *result = maximum < 0 ? NULL : malloc((size_t)maximum + 1);
  if (result != NULL && !CFStringGetCString(value, result, maximum + 1, kCFStringEncodingUTF8)) {
    free(result);
    result = NULL;
  }
  CFRelease(value);
  return result;
}

static bool wd_ca_device_has_input(AudioDeviceID device) {
  AudioObjectPropertyAddress address = {
    kAudioDevicePropertyStreams,
    kAudioDevicePropertyScopeInput,
    kAudioObjectPropertyElementMain,
  };
  UInt32 size = 0;
  return AudioObjectGetPropertyDataSize(device, &address, 0, NULL, &size) == noErr &&
         size >= sizeof(AudioStreamID);
}

static bool wd_ca_device_alive(AudioDeviceID device) {
  AudioObjectPropertyAddress address = {
    kAudioDevicePropertyDeviceIsAlive,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  UInt32 alive = 0;
  UInt32 size = sizeof(alive);
  return AudioObjectGetPropertyData(device, &address, 0, NULL, &size, &alive) == noErr && alive != 0;
}

static int wd_ca_device_ids(AudioDeviceID **out, size_t *count, char **error) {
  if (out == NULL || count == NULL) return WD_CA_STATUS_INVALID;
  *out = NULL;
  *count = 0;
  AudioObjectPropertyAddress address = {
    kAudioHardwarePropertyDevices,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  UInt32 size = 0;
  OSStatus status = AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &address, 0, NULL, &size);
  if (status != noErr) {
    wd_ca_set_error(error, "enumerate CoreAudio devices: OSStatus %d", (int)status);
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
  if (size == 0) return WD_CA_STATUS_OK;
  if (size % sizeof(AudioDeviceID) != 0) {
    wd_ca_set_error(error, "enumerate CoreAudio devices: invalid property size %u", size);
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
  AudioDeviceID *ids = malloc(size);
  if (ids == NULL) return WD_CA_STATUS_NO_MEMORY;
  status = AudioObjectGetPropertyData(kAudioObjectSystemObject, &address, 0, NULL, &size, ids);
  if (status != noErr) {
    free(ids);
    wd_ca_set_error(error, "enumerate CoreAudio devices: OSStatus %d", (int)status);
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
  *out = ids;
  *count = size / sizeof(AudioDeviceID);
  return WD_CA_STATUS_OK;
}

static AudioDeviceID wd_ca_default_input(void) {
  AudioObjectPropertyAddress address = {
    kAudioHardwarePropertyDefaultInputDevice,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  AudioDeviceID device = kAudioObjectUnknown;
  UInt32 size = sizeof(device);
  if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &address, 0, NULL, &size, &device) != noErr) {
    return kAudioObjectUnknown;
  }
  return device;
}

static void wd_ca_device_info_destroy(wd_ca_device_info *info) {
  if (info == NULL) return;
  free(info->uid);
  free(info->name);
  memset(info, 0, sizeof(*info));
}

static bool wd_ca_read_device_info(AudioDeviceID device, wd_ca_device_info *info) {
  memset(info, 0, sizeof(*info));
  if (device == kAudioObjectUnknown) return false;
  info->id = device;
  info->uid = wd_ca_copy_cf_string_property(device, kAudioDevicePropertyDeviceUID,
                                             kAudioObjectPropertyScopeGlobal);
  info->name = wd_ca_copy_cf_string_property(device, kAudioObjectPropertyName,
                                              kAudioObjectPropertyScopeGlobal);
  info->alive = wd_ca_device_alive(device);
  info->input = wd_ca_device_has_input(device);
  if (info->uid == NULL || info->name == NULL) {
    wd_ca_device_info_destroy(info);
    return false;
  }
  return true;
}

static int wd_ca_resolve_device(const char *requested, wd_ca_device_info *info, char **error) {
  bool explicit = requested != NULL && requested[0] != '\0';
  if (!explicit) {
    AudioDeviceID device = wd_ca_default_input();
    if (!wd_ca_read_device_info(device, info) || !info->input) {
      wd_ca_device_info_destroy(info);
      wd_ca_set_error(error, "resolve default CoreAudio input: no usable default input device");
      return WD_CA_STATUS_DEVICE_NOT_FOUND;
    }
    if (!info->alive) {
      wd_ca_device_info_destroy(info);
      wd_ca_set_error(error, "resolve default CoreAudio input: device is disconnected");
      return WD_CA_STATUS_DEVICE_DISCONNECTED;
    }
    return WD_CA_STATUS_OK;
  }

  AudioDeviceID *ids = NULL;
  size_t count = 0;
  int result = wd_ca_device_ids(&ids, &count, error);
  if (result != WD_CA_STATUS_OK) return result;
  for (size_t i = 0; i < count; i++) {
    wd_ca_device_info candidate;
    if (!wd_ca_read_device_info(ids[i], &candidate)) continue;
    bool match = strcmp(candidate.uid, requested) == 0;
    if (match) {
      free(ids);
      if (!candidate.input) {
        wd_ca_device_info_destroy(&candidate);
        wd_ca_set_error(error, "CoreAudio device %s has no input streams", requested);
        return WD_CA_STATUS_DEVICE_NOT_FOUND;
      }
      if (!candidate.alive) {
        wd_ca_device_info_destroy(&candidate);
        wd_ca_set_error(error, "CoreAudio device %s is disconnected", requested);
        return WD_CA_STATUS_DEVICE_DISCONNECTED;
      }
      *info = candidate;
      return WD_CA_STATUS_OK;
    }
    wd_ca_device_info_destroy(&candidate);
  }
  free(ids);
  wd_ca_set_error(error, "CoreAudio input device %s was not found", requested);
  return WD_CA_STATUS_DEVICE_NOT_FOUND;
}

int wd_ca_copy_devices(wd_ca_device_list **out, char **error) {
  if (out == NULL) return WD_CA_STATUS_INVALID;
  *out = NULL;
  @try {
    @autoreleasepool {
      AudioDeviceID *ids = NULL;
      size_t count = 0;
      int result = wd_ca_device_ids(&ids, &count, error);
      if (result != WD_CA_STATUS_OK) return result;
      wd_ca_device_list *list = calloc(1, sizeof(*list));
      if (list == NULL) {
        free(ids);
        return WD_CA_STATUS_NO_MEMORY;
      }
      if (count != 0) {
        list->items = calloc(count, sizeof(*list->items));
        if (list->items == NULL) {
          free(ids);
          free(list);
          return WD_CA_STATUS_NO_MEMORY;
        }
      }
      AudioDeviceID default_device = wd_ca_default_input();
      for (size_t i = 0; i < count; i++) {
        wd_ca_device_info info;
        if (!wd_ca_read_device_info(ids[i], &info) || !info.input) {
          wd_ca_device_info_destroy(&info);
          continue;
        }
        wd_ca_device *device = &list->items[list->count++];
        device->uid = info.uid;
        device->name = info.name;
        device->is_default = info.id == default_device;
        device->connected = info.alive;
        info.uid = NULL;
        info.name = NULL;
        wd_ca_device_info_destroy(&info);
      }
      free(ids);
      *out = list;
      return WD_CA_STATUS_OK;
    }
  } @catch (NSException *exception) {
    wd_ca_set_error(error, "enumerate CoreAudio devices: %s", exception.reason.UTF8String ?: "Objective-C exception");
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
}

void wd_ca_device_list_destroy(wd_ca_device_list *list) {
  if (list == NULL) return;
  for (size_t i = 0; i < list->count; i++) {
    free((void *)list->items[i].uid);
    free((void *)list->items[i].name);
  }
  free(list->items);
  free(list);
}

int wd_ca_permission_state(wd_ca_permission *permission, char **error) {
  if (permission == NULL) return WD_CA_STATUS_INVALID;
  @try {
    @autoreleasepool {
      switch ([AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio]) {
        case AVAuthorizationStatusNotDetermined:
          *permission = WD_CA_PERMISSION_NOT_DETERMINED;
          break;
        case AVAuthorizationStatusRestricted:
          *permission = WD_CA_PERMISSION_RESTRICTED;
          break;
        case AVAuthorizationStatusDenied:
          *permission = WD_CA_PERMISSION_DENIED;
          break;
        case AVAuthorizationStatusAuthorized:
          *permission = WD_CA_PERMISSION_GRANTED;
          break;
      }
      return WD_CA_STATUS_OK;
    }
  } @catch (NSException *exception) {
    wd_ca_set_error(error, "read microphone permission: %s", exception.reason.UTF8String ?: "Objective-C exception");
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
}

static void wd_ca_queue_event(wd_ca_capture *capture,
                              wd_ca_event_kind kind,
                              wd_ca_status status,
                              const char *uid) {
  pthread_mutex_lock(&capture->event_mutex);
  bool overflow = capture->event_count == WD_CA_EVENT_CAPACITY;
  if (overflow) {
    capture->event_overflow = true;
  } else {
    size_t tail = (capture->event_head + capture->event_count) % WD_CA_EVENT_CAPACITY;
    wd_ca_event *event = &capture->events[tail];
    memset(event, 0, sizeof(*event));
    event->kind = kind;
    event->status = status;
    if (uid != NULL) {
      strncpy(event->device_uid, uid, sizeof(event->device_uid) - 1);
    }
    capture->event_count++;
  }
  pthread_cond_broadcast(&capture->event_cond);
  pthread_mutex_unlock(&capture->event_mutex);
  if (overflow) {
    (void)wd_ca_store_terminal(capture, WD_CA_STATUS_BACKEND_UNAVAILABLE);
    wd_ca_wake_readers(capture);
  }
}

static void wd_ca_queue_current_event(wd_ca_capture *capture,
                                      wd_ca_event_kind kind,
                                      wd_ca_status status) {
  char uid[512] = {0};
  pthread_mutex_lock(&capture->device_mutex);
  if (capture->device_uid != NULL) {
    strncpy(uid, capture->device_uid, sizeof(uid) - 1);
  }
  pthread_mutex_unlock(&capture->device_mutex);
  wd_ca_queue_event(capture, kind, status, uid);
}

static void wd_ca_wake_readers(wd_ca_capture *capture) {
  pthread_mutex_lock(&capture->wait_mutex);
  pthread_cond_broadcast(&capture->wait_cond);
  pthread_cond_broadcast(&capture->start_cond);
  pthread_mutex_unlock(&capture->wait_mutex);
}

static bool wd_ca_store_terminal(wd_ca_capture *capture, wd_ca_status status) {
  int expected = WD_CA_STATUS_OK;
  bool stored = atomic_compare_exchange_strong_explicit(&capture->terminal_status,
                                                         &expected,
                                                         status,
                                                         memory_order_release,
                                                         memory_order_relaxed);
  if (stored && atomic_load_explicit(&capture->semaphore_created, memory_order_acquire)) {
    semaphore_signal(capture->raw_semaphore);
  }
  return stored;
}

static void wd_ca_store_terminal_and_wake(wd_ca_capture *capture, wd_ca_status status) {
  if (wd_ca_store_terminal(capture, status)) wd_ca_wake_readers(capture);
}

static void wd_ca_set_device_strings(wd_ca_capture *capture,
                                     AudioDeviceID device,
                                     const char *uid,
                                     const char *name) {
  char *uid_copy = uid == NULL ? NULL : strdup(uid);
  char *name_copy = name == NULL ? NULL : strdup(name);
  pthread_mutex_lock(&capture->device_mutex);
  free(capture->device_uid);
  free(capture->device_name);
  capture->device_uid = uid_copy;
  capture->device_name = name_copy;
  atomic_store_explicit(&capture->selected_device, device, memory_order_release);
  pthread_mutex_unlock(&capture->device_mutex);
}

static void wd_ca_refresh_default_device(wd_ca_capture *capture) {
  wd_ca_device_info info;
  if (wd_ca_read_device_info(wd_ca_default_input(), &info) && info.input && info.alive) {
    wd_ca_set_device_strings(capture, info.id, info.uid, info.name);
  }
  wd_ca_device_info_destroy(&info);
}

static void wd_ca_remove_device_listeners(wd_ca_capture *capture) {
  if (!capture->device_listeners_registered || capture->listener_device == kAudioObjectUnknown) return;
  AudioObjectPropertyAddress alive = {
    kAudioDevicePropertyDeviceIsAlive,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  AudioObjectPropertyAddress streams = {
    kAudioDevicePropertyStreamConfiguration,
    kAudioDevicePropertyScopeInput,
    kAudioObjectPropertyElementMain,
  };
  AudioObjectPropertyAddress rate = {
    kAudioDevicePropertyNominalSampleRate,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  (void)AudioObjectRemovePropertyListener(capture->listener_device, &alive, wd_ca_property_listener, capture);
  (void)AudioObjectRemovePropertyListener(capture->listener_device, &streams, wd_ca_property_listener, capture);
  (void)AudioObjectRemovePropertyListener(capture->listener_device, &rate, wd_ca_property_listener, capture);
  capture->device_listeners_registered = false;
  capture->listener_device = kAudioObjectUnknown;
}

static int wd_ca_install_device_listeners(wd_ca_capture *capture, AudioDeviceID device, char **error) {
  if (capture->device_listeners_registered && capture->listener_device == device) return WD_CA_STATUS_OK;
  wd_ca_remove_device_listeners(capture);
  AudioObjectPropertyAddress alive = {
    kAudioDevicePropertyDeviceIsAlive,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  AudioObjectPropertyAddress streams = {
    kAudioDevicePropertyStreamConfiguration,
    kAudioDevicePropertyScopeInput,
    kAudioObjectPropertyElementMain,
  };
  AudioObjectPropertyAddress rate = {
    kAudioDevicePropertyNominalSampleRate,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  OSStatus status = AudioObjectAddPropertyListener(device, &alive, wd_ca_property_listener, capture);
  if (status == noErr) status = AudioObjectAddPropertyListener(device, &streams, wd_ca_property_listener, capture);
  if (status == noErr) status = AudioObjectAddPropertyListener(device, &rate, wd_ca_property_listener, capture);
  if (status != noErr) {
    (void)AudioObjectRemovePropertyListener(device, &alive, wd_ca_property_listener, capture);
    (void)AudioObjectRemovePropertyListener(device, &streams, wd_ca_property_listener, capture);
    (void)AudioObjectRemovePropertyListener(device, &rate, wd_ca_property_listener, capture);
    wd_ca_set_error(error, "register CoreAudio device listeners: OSStatus %d", (int)status);
    return WD_CA_STATUS_START_FAILED;
  }
  capture->device_listeners_registered = true;
  capture->listener_device = device;
  return WD_CA_STATUS_OK;
}

static bool wd_ca_note_startup_format_change(wd_ca_capture *capture) {
  if (atomic_load_explicit(&capture->state, memory_order_acquire) != WD_CA_STATE_STARTING) {
    return false;
  }
  int phase = WD_CA_START_PENDING;
  if (atomic_compare_exchange_strong_explicit(&capture->start_phase,
                                              &phase,
                                              WD_CA_START_REARM,
                                              memory_order_acq_rel,
                                              memory_order_acquire)) {
    return true;
  }
  return phase == WD_CA_START_REARM;
}

static bool wd_ca_confirm_startup(wd_ca_capture *capture) {
  int phase = WD_CA_START_PENDING;
  return atomic_compare_exchange_strong_explicit(&capture->start_phase,
                                                  &phase,
                                                  WD_CA_START_CONFIRMED,
                                                  memory_order_acq_rel,
                                                  memory_order_acquire);
}

static OSStatus wd_ca_property_listener(AudioObjectID object,
                                        UInt32 address_count,
                                        const AudioObjectPropertyAddress addresses[],
                                        void *context) {
  wd_ca_capture *capture = context;
  if (capture == NULL) return noErr;
  @autoreleasepool {
    @try {
      for (UInt32 i = 0; i < address_count; i++) {
        AudioObjectPropertySelector selector = addresses[i].mSelector;
        if (object == kAudioObjectSystemObject && selector == kAudioHardwarePropertyDefaultInputDevice) {
          if (!atomic_load_explicit(&capture->explicit_device, memory_order_acquire)) {
            int state = atomic_load_explicit(&capture->state, memory_order_acquire);
            if (state == WD_CA_STATE_STARTING || state == WD_CA_STATE_CAPTURING) {
              wd_ca_queue_current_event(capture, WD_CA_EVENT_DEFAULT_CHANGED, WD_CA_STATUS_DEVICE_CHANGED);
              wd_ca_store_terminal_and_wake(capture, WD_CA_STATUS_DEVICE_CHANGED);
            } else if (state == WD_CA_STATE_IDLE) {
              atomic_store_explicit(&capture->default_device_dirty, true, memory_order_release);
              wd_ca_queue_current_event(capture, WD_CA_EVENT_DEFAULT_CHANGED, WD_CA_STATUS_OK);
            }
          }
        } else if (object == atomic_load_explicit(&capture->selected_device, memory_order_acquire) &&
                   selector == kAudioDevicePropertyDeviceIsAlive) {
          AudioDeviceID selected = atomic_load_explicit(&capture->selected_device, memory_order_acquire);
          bool alive = wd_ca_device_alive(selected);
          wd_ca_queue_current_event(capture,
                                    alive ? WD_CA_EVENT_DEVICE_ADDED : WD_CA_EVENT_DEVICE_REMOVED,
                                    alive ? WD_CA_STATUS_OK : WD_CA_STATUS_DEVICE_DISCONNECTED);
          if (!alive) wd_ca_store_terminal_and_wake(capture, WD_CA_STATUS_DEVICE_DISCONNECTED);
        } else if (object == atomic_load_explicit(&capture->selected_device, memory_order_acquire) &&
                   (selector == kAudioDevicePropertyStreamConfiguration ||
                    selector == kAudioDevicePropertyNominalSampleRate)) {
          atomic_fetch_add_explicit(&capture->format_change_events, 1, memory_order_relaxed);
          int state = atomic_load_explicit(&capture->state, memory_order_acquire);
          bool settling = wd_ca_note_startup_format_change(capture);
          wd_ca_queue_current_event(capture,
                                    WD_CA_EVENT_FORMAT_CHANGED,
                                    settling ? WD_CA_STATUS_OK : WD_CA_STATUS_FORMAT_CHANGED);
          if (settling) {
            atomic_fetch_add_explicit(&capture->ignored_startup_format_events,
                                      1,
                                      memory_order_relaxed);
            wd_ca_wake_readers(capture);
          } else if (state == WD_CA_STATE_STARTING || state == WD_CA_STATE_CAPTURING) {
            wd_ca_store_terminal_and_wake(capture, WD_CA_STATUS_FORMAT_CHANGED);
          }
        }
      }
    } @catch (__unused NSException *exception) {
      wd_ca_store_terminal_and_wake(capture, WD_CA_STATUS_BACKEND_UNAVAILABLE);
    }
  }
  return noErr;
}

static void wd_ca_update_level(wd_ca_capture *capture, float *samples, size_t count) {
  if (count == 0) {
    atomic_store_explicit(&capture->level_mdb, -120000, memory_order_relaxed);
    return;
  }
  double sum = 0.0;
  for (size_t i = 0; i < count; i++) {
    float sample = samples[i];
    if (!isfinite(sample)) sample = 0.0f;
    if (sample > 1.0f) sample = 1.0f;
    if (sample < -1.0f) sample = -1.0f;
    samples[i] = sample;
    sum += (double)sample * (double)sample;
  }
  double rms = sqrt(sum / (double)count);
  double db = rms > 0.0 ? 20.0 * log10(rms) : WD_CA_LEVEL_FLOOR;
  if (db < WD_CA_LEVEL_FLOOR) db = WD_CA_LEVEL_FLOOR;
  atomic_store_explicit(&capture->level_mdb, (int)lrint(db * 1000.0), memory_order_relaxed);
}

static void wd_ca_publish_converted(wd_ca_capture *capture, float *samples, size_t count) {
  if (count == 0) return;
  wd_ca_update_level(capture, samples, count);
  if (!wd_ca_float_ring_write(&capture->processed, samples, count)) {
    atomic_fetch_add_explicit(&capture->overruns, 1, memory_order_relaxed);
    wd_ca_queue_current_event(capture, WD_CA_EVENT_OVERRUN, WD_CA_STATUS_OK);
    return;
  }
  pthread_mutex_lock(&capture->wait_mutex);
  pthread_cond_signal(&capture->wait_cond);
  pthread_mutex_unlock(&capture->wait_mutex);
}

static bool wd_ca_load_raw_slot(wd_ca_capture *capture,
                                WDCASession *session,
                                wd_ca_raw_slot *slot) {
  AudioBufferList *buffers = session->_inputScratch.mutableAudioBufferList;
  if (buffers == NULL || buffers->mNumberBuffers != slot->buffer_count ||
      slot->buffer_count != capture->raw.buffer_count || slot->frame_count > capture->tap_frames) {
    return false;
  }
  for (uint32_t i = 0; i < slot->buffer_count; i++) {
    size_t expected;
    if (!wd_ca_checked_mul(capture->raw.bytes_per_frame[i], slot->frame_count, &expected) ||
        expected != slot->byte_counts[i] ||
        buffers->mBuffers[i].mData == NULL) {
      return false;
    }
    memcpy(buffers->mBuffers[i].mData, wd_ca_raw_slot_buffer(&capture->raw, slot, i), expected);
    buffers->mBuffers[i].mDataByteSize = (UInt32)expected;
  }
  session->_inputScratch.frameLength = slot->frame_count;
  return true;
}

static bool wd_ca_convert_slot(wd_ca_capture *capture,
                               WDCASession *session,
                               wd_ca_raw_slot *slot,
                               uint64_t *produced_frames) {
  *produced_frames = 0;
  if (!wd_ca_load_raw_slot(capture, session, slot)) return false;
  session->_inputProvided = NO;
  NSUInteger iterations = 0;
  for (;;) {
    if (++iterations > 16) return false;
    session->_outputScratch.frameLength = 0;
    NSError *error = nil;
    AVAudioConverterOutputStatus status =
      [session->_converter convertToBuffer:session->_outputScratch
                                     error:&error
                        withInputFromBlock:session->_inputBlock];
    AVAudioFrameCount frames = session->_outputScratch.frameLength;
    if (frames != 0) {
      float *samples = session->_outputScratch.floatChannelData[0];
      if (samples == NULL) return false;
      atomic_fetch_add_explicit(&capture->converted_buffers, 1, memory_order_relaxed);
      atomic_fetch_add_explicit(&capture->converted_frames, frames, memory_order_relaxed);
      *produced_frames += frames;
      wd_ca_publish_converted(capture, samples, frames);
    }
    if (status == AVAudioConverterOutputStatus_Error || error != nil) return false;
    if (status != AVAudioConverterOutputStatus_HaveData) break;
  }
  return true;
}

static void *wd_ca_conversion_main(void *context) {
  wd_ca_capture *capture = context;
  @autoreleasepool {
    @try {
      WDCASession *session = (__bridge WDCASession *)capture->session_ref;
      uint64_t reported_overruns = atomic_load_explicit(&capture->overruns, memory_order_relaxed);
      while (!atomic_load_explicit(&capture->conversion_exit, memory_order_acquire)) {
        (void)semaphore_wait(capture->raw_semaphore);
        if (atomic_load_explicit(&capture->conversion_exit, memory_order_acquire)) break;
        if (atomic_load_explicit(&capture->start_phase, memory_order_acquire) == WD_CA_START_REARM) {
          wd_ca_wake_readers(capture);
          break;
        }
        int terminal = atomic_load_explicit(&capture->terminal_status, memory_order_acquire);
        if (terminal != WD_CA_STATUS_OK) break;
        wd_ca_raw_slot *slot = NULL;
        uint64_t sequence = 0;
        while (wd_ca_raw_ring_peek(&capture->raw, &slot, &sequence)) {
          if (atomic_load_explicit(&capture->conversion_exit, memory_order_acquire)) break;
          uint64_t produced_frames = 0;
          if (!wd_ca_convert_slot(capture, session, slot, &produced_frames)) {
            wd_ca_store_terminal(capture, WD_CA_STATUS_CONVERTER_FAILED);
            break;
          }
          wd_ca_raw_ring_release(&capture->raw, sequence);
          if (produced_frames != 0 && wd_ca_confirm_startup(capture)) {
            pthread_mutex_lock(&capture->wait_mutex);
            pthread_cond_broadcast(&capture->start_cond);
            pthread_mutex_unlock(&capture->wait_mutex);
          }
        }
        uint64_t overruns = atomic_load_explicit(&capture->overruns, memory_order_relaxed);
        if (overruns != reported_overruns) {
          reported_overruns = overruns;
          wd_ca_queue_current_event(capture, WD_CA_EVENT_OVERRUN, WD_CA_STATUS_OK);
        }
        if (atomic_load_explicit(&capture->terminal_status, memory_order_acquire) != WD_CA_STATUS_OK) break;
      }
    } @catch (__unused NSException *exception) {
      wd_ca_store_terminal(capture, WD_CA_STATUS_CONVERTER_FAILED);
    }
  }
  if (atomic_load_explicit(&capture->terminal_status, memory_order_acquire) != WD_CA_STATUS_OK) {
    wd_ca_wake_readers(capture);
  }
  return NULL;
}

static void wd_ca_close_tap_gate(WDCATapContext *context) {
  if (context == nil) return;
  atomic_store_explicit(&context->_closing, true, memory_order_seq_cst);
  atomic_store_explicit(&context->_capture, NULL, memory_order_seq_cst);
}

static void wd_ca_record_teardown_error(wd_ca_capture *capture, const char *operation) {
  if (capture->teardown_result == WD_CA_STATUS_OK) {
    capture->teardown_result = WD_CA_STATUS_START_FAILED;
    snprintf(capture->teardown_detail, sizeof(capture->teardown_detail), "%s", operation);
  }
}

static void wd_ca_release_session_reference(wd_ca_capture *capture) {
  void *reference = capture->session_ref;
  capture->session_ref = NULL;
  if (reference == NULL) return;
  @try {
    @autoreleasepool {
      WDCASession *released = (__bridge_transfer WDCASession *)reference;
      (void)released;
      released = nil;
    }
  } @catch (__unused NSException *exception) {
    wd_ca_record_teardown_error(capture, "release CoreAudio engine objects");
  }
}

static void wd_ca_clear_native_session(wd_ca_capture *capture) {
  wd_ca_float_ring_clear(&capture->processed);
  atomic_store_explicit(&capture->start_phase, WD_CA_START_PENDING, memory_order_relaxed);
  atomic_store_explicit(&capture->level_mdb, -120000, memory_order_relaxed);
  capture->tap_frames = 0;
  capture->quantum_ms = 0;
  capture->input_buffers = 0;
  capture->input_latency_seconds = 0;
}

static void wd_ca_dispose_unstarted_session(wd_ca_capture *capture) {
  __unsafe_unretained WDCASession *session = capture->session_ref == NULL
    ? nil
    : (__bridge WDCASession *)capture->session_ref;
  if (session != nil) wd_ca_close_tap_gate(session->_tapContext);
  if (atomic_load_explicit(&capture->semaphore_created, memory_order_acquire)) {
    atomic_store_explicit(&capture->semaphore_created, false, memory_order_release);
    semaphore_destroy(mach_task_self(), capture->raw_semaphore);
  }
  wd_ca_raw_ring_destroy(&capture->raw);
  wd_ca_release_session_reference(capture);
  wd_ca_clear_native_session(capture);
}

static int wd_ca_teardown_session(wd_ca_capture *capture) {
  capture->teardown_result = WD_CA_STATUS_OK;
  capture->teardown_detail[0] = '\0';
  __unsafe_unretained WDCASession *session = capture->session_ref == NULL
    ? nil
    : (__bridge WDCASession *)capture->session_ref;
  __unsafe_unretained WDCATapContext *context = session == nil ? nil : session->_tapContext;

  if (session != nil && session->_tapInstalled) {
    @try {
      [session->_inputNode removeTapOnBus:0];
    } @catch (__unused NSException *exception) {
      wd_ca_record_teardown_error(capture, "remove CoreAudio input tap");
    }
    session->_tapInstalled = NO;
  }
  if (session != nil && session->_engine != nil) {
    @try {
      [session->_engine stop];
    } @catch (__unused NSException *exception) {
      wd_ca_record_teardown_error(capture, "stop CoreAudio engine");
    }
  }

  while (context != nil &&
         atomic_load_explicit(&context->_callbacks, memory_order_seq_cst) != 0) {
    sched_yield();
  }
  if (capture->conversion_thread_started) {
    (void)pthread_join(capture->conversion_thread, NULL);
    capture->conversion_thread_started = false;
  }
  wd_ca_log_diagnostics(capture,
                        session,
                        "teardown",
                        capture->teardown_result == WD_CA_STATUS_OK ? "complete" : "error",
                        capture->teardown_result,
                        0);
  if (atomic_load_explicit(&capture->semaphore_created, memory_order_acquire)) {
    atomic_store_explicit(&capture->semaphore_created, false, memory_order_release);
    semaphore_destroy(mach_task_self(), capture->raw_semaphore);
  }
  wd_ca_raw_ring_destroy(&capture->raw);
  wd_ca_clear_native_session(capture);
  wd_ca_release_session_reference(capture);
  return capture->teardown_result;
}

static void *wd_ca_teardown_main(void *context) {
  wd_ca_capture *capture = context;
  pthread_mutex_lock(&capture->lifecycle_mutex);
  while (!capture->teardown_requested) {
    pthread_cond_wait(&capture->lifecycle_cond, &capture->lifecycle_mutex);
  }
  pthread_mutex_unlock(&capture->lifecycle_mutex);

  int result = WD_CA_STATUS_OK;
  @autoreleasepool {
    result = wd_ca_teardown_session(capture);
  }
  if (result != WD_CA_STATUS_OK) {
    atomic_store_explicit(&capture->stop_requested, true, memory_order_release);
    (void)wd_ca_store_terminal(capture, (wd_ca_status)result);
  }
  bool stopping = atomic_load_explicit(&capture->stop_requested, memory_order_acquire);
  if (!stopping && result == WD_CA_STATUS_OK) {
    atomic_store_explicit(&capture->terminal_status, WD_CA_STATUS_OK, memory_order_release);
    atomic_store_explicit(&capture->state, WD_CA_STATE_IDLE, memory_order_release);
  } else {
    atomic_store_explicit(&capture->state, WD_CA_STATE_STOPPING, memory_order_release);
  }

  wd_ca_wake_readers(capture);
  pthread_mutex_lock(&capture->event_mutex);
  pthread_cond_broadcast(&capture->event_cond);
  pthread_mutex_unlock(&capture->event_mutex);
  pthread_mutex_lock(&capture->lifecycle_mutex);
  capture->teardown_result = result;
  capture->teardown_done = true;
  bool destroy = capture->destroy_requested;
  pthread_cond_broadcast(&capture->lifecycle_cond);
  pthread_mutex_unlock(&capture->lifecycle_mutex);
  if (destroy) wd_ca_finalize_destroy(capture);
  return NULL;
}

static int wd_ca_start_teardown_thread(wd_ca_capture *capture, char **error) {
  pthread_mutex_lock(&capture->lifecycle_mutex);
  capture->teardown_requested = false;
  capture->teardown_done = false;
  capture->teardown_detached = false;
  capture->destroy_requested = false;
  capture->teardown_result = WD_CA_STATUS_OK;
  capture->teardown_detail[0] = '\0';
  int result = pthread_create(&capture->teardown_thread, NULL, wd_ca_teardown_main, capture);
  capture->teardown_thread_started = result == 0;
  pthread_mutex_unlock(&capture->lifecycle_mutex);
  if (result != 0) {
    wd_ca_set_error(error, "start CoreAudio teardown thread: pthread error %d", result);
    return WD_CA_STATUS_START_FAILED;
  }
  return WD_CA_STATUS_OK;
}

static void wd_ca_gate_session(wd_ca_capture *capture) {
  __unsafe_unretained WDCASession *session = capture->session_ref == NULL
    ? nil
    : (__bridge WDCASession *)capture->session_ref;
  if (session != nil) wd_ca_close_tap_gate(session->_tapContext);
  atomic_store_explicit(&capture->conversion_exit, true, memory_order_release);
  if (atomic_load_explicit(&capture->semaphore_created, memory_order_acquire)) {
    semaphore_signal(capture->raw_semaphore);
  }
}

static int wd_ca_request_teardown(wd_ca_capture *capture,
                                  bool stop,
                                  uint32_t timeout_ms,
                                  char **error) {
  if (stop) atomic_store_explicit(&capture->stop_requested, true, memory_order_release);
  bool stopping = atomic_load_explicit(&capture->stop_requested, memory_order_acquire);
  atomic_store_explicit(&capture->state,
                        stopping ? WD_CA_STATE_STOPPING : WD_CA_STATE_PAUSING,
                        memory_order_release);

  pthread_mutex_lock(&capture->lifecycle_mutex);
  if (!capture->teardown_thread_started) {
    pthread_mutex_unlock(&capture->lifecycle_mutex);
    wd_ca_set_error(error, "CoreAudio teardown worker is unavailable");
    return WD_CA_STATUS_START_FAILED;
  }
  if (!capture->teardown_requested) wd_ca_gate_session(capture);
  capture->teardown_requested = true;
  pthread_cond_broadcast(&capture->lifecycle_cond);
  struct timespec deadline = wd_ca_deadline(timeout_ms);
  while (!capture->teardown_done) {
    int wait_result = pthread_cond_timedwait(&capture->lifecycle_cond,
                                             &capture->lifecycle_mutex,
                                             &deadline);
    if (wait_result == ETIMEDOUT) break;
  }
  if (!capture->teardown_done) {
    atomic_store_explicit(&capture->stop_requested, true, memory_order_release);
    atomic_store_explicit(&capture->state, WD_CA_STATE_STOPPING, memory_order_release);
    (void)wd_ca_store_terminal(capture, WD_CA_STATUS_TEARDOWN_TIMEOUT);
    if (!capture->teardown_detached) {
      capture->teardown_detached = pthread_detach(capture->teardown_thread) == 0;
    }
    pthread_mutex_unlock(&capture->lifecycle_mutex);
    pthread_cond_broadcast(&capture->wait_cond);
    pthread_cond_broadcast(&capture->start_cond);
    pthread_cond_broadcast(&capture->event_cond);
    wd_ca_set_error(error, "CoreAudio teardown exceeded %u ms; cleanup continues asynchronously", timeout_ms);
    return WD_CA_STATUS_TEARDOWN_TIMEOUT;
  }

  int result = capture->teardown_result;
  char detail[sizeof(capture->teardown_detail)];
  memcpy(detail, capture->teardown_detail, sizeof(detail));
  pthread_t thread = capture->teardown_thread;
  bool detached = capture->teardown_detached;
  capture->teardown_thread_started = false;
  pthread_mutex_unlock(&capture->lifecycle_mutex);
  if (!detached) (void)pthread_join(thread, NULL);
  if (result != WD_CA_STATUS_OK) {
    wd_ca_set_error(error, "%s", detail[0] == '\0' ? "CoreAudio teardown failed" : detail);
  }
  return result;
}

static void wd_ca_reset_session(wd_ca_capture *capture) {
  wd_ca_clear_native_session(capture);
  capture->teardown_result = WD_CA_STATUS_OK;
  capture->teardown_detail[0] = '\0';
  atomic_store_explicit(&capture->conversion_exit, false, memory_order_relaxed);
  atomic_store_explicit(&capture->terminal_status, WD_CA_STATUS_OK, memory_order_relaxed);
  atomic_store_explicit(&capture->tap_callbacks, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->tap_frames_seen, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->raw_buffers, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->raw_frames, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->converted_buffers, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->converted_frames, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->format_change_events, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->ignored_startup_format_events, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->tap_max_frames, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->tap_last_buffer_count, 0, memory_order_relaxed);
  atomic_store_explicit(&capture->tap_failure, WD_CA_TAP_FAILURE_NONE, memory_order_relaxed);
}

int wd_ca_capture_create(uint32_t sample_rate,
                         uint32_t ring_seconds,
                         wd_ca_capture **out,
                         char **error) {
  if (out == NULL || sample_rate != 16000 || ring_seconds == 0) {
    wd_ca_set_error(error, "CoreAudio output must be 16000 Hz with a positive ring duration");
    return WD_CA_STATUS_INVALID;
  }
  *out = NULL;
  size_t frames;
  if (!wd_ca_checked_mul(sample_rate, ring_seconds, &frames)) {
    wd_ca_set_error(error, "CoreAudio processed ring capacity overflow");
    return WD_CA_STATUS_INVALID;
  }
  wd_ca_capture *capture = calloc(1, sizeof(*capture));
  if (capture == NULL) return WD_CA_STATUS_NO_MEMORY;
  if (pthread_mutex_init(&capture->wait_mutex, NULL) != 0) goto allocation_failed;
  capture->wait_mutex_initialized = true;
  if (pthread_cond_init(&capture->wait_cond, NULL) != 0) goto allocation_failed;
  capture->wait_cond_initialized = true;
  if (pthread_cond_init(&capture->start_cond, NULL) != 0) goto allocation_failed;
  capture->start_cond_initialized = true;
  if (pthread_mutex_init(&capture->event_mutex, NULL) != 0) goto allocation_failed;
  capture->event_mutex_initialized = true;
  if (pthread_cond_init(&capture->event_cond, NULL) != 0) goto allocation_failed;
  capture->event_cond_initialized = true;
  if (pthread_mutex_init(&capture->device_mutex, NULL) != 0) goto allocation_failed;
  capture->device_mutex_initialized = true;
  if (pthread_mutex_init(&capture->lifecycle_mutex, NULL) != 0) goto allocation_failed;
  capture->lifecycle_mutex_initialized = true;
  if (pthread_cond_init(&capture->lifecycle_cond, NULL) != 0) goto allocation_failed;
  capture->lifecycle_cond_initialized = true;
  if (!wd_ca_float_ring_init(&capture->processed, frames)) goto allocation_failed;
  {
    capture->sample_rate = sample_rate;
    atomic_init(&capture->selected_device, kAudioObjectUnknown);
    capture->listener_device = kAudioObjectUnknown;
    atomic_init(&capture->state, WD_CA_STATE_IDLE);
    atomic_init(&capture->terminal_status, WD_CA_STATUS_OK);
    atomic_init(&capture->conversion_exit, false);
    atomic_init(&capture->start_phase, WD_CA_START_PENDING);
    atomic_init(&capture->generation, 0);
    atomic_init(&capture->overruns, 0);
    atomic_init(&capture->tap_callbacks, 0);
    atomic_init(&capture->tap_frames_seen, 0);
    atomic_init(&capture->raw_buffers, 0);
    atomic_init(&capture->raw_frames, 0);
    atomic_init(&capture->converted_buffers, 0);
    atomic_init(&capture->converted_frames, 0);
    atomic_init(&capture->format_change_events, 0);
    atomic_init(&capture->ignored_startup_format_events, 0);
    atomic_init(&capture->tap_max_frames, 0);
    atomic_init(&capture->tap_last_buffer_count, 0);
    atomic_init(&capture->tap_failure, WD_CA_TAP_FAILURE_NONE);
    atomic_init(&capture->level_mdb, -120000);
    atomic_init(&capture->default_device_dirty, false);
    atomic_init(&capture->semaphore_created, false);
    atomic_init(&capture->explicit_device, false);
    atomic_init(&capture->stop_requested, false);
  }

  AudioObjectPropertyAddress address = {
    kAudioHardwarePropertyDefaultInputDevice,
    kAudioObjectPropertyScopeGlobal,
    kAudioObjectPropertyElementMain,
  };
  OSStatus status = AudioObjectAddPropertyListener(kAudioObjectSystemObject,
                                                    &address,
                                                    wd_ca_property_listener,
                                                    capture);
  if (status != noErr) {
    wd_ca_set_error(error, "register default-input listener: OSStatus %d", (int)status);
    wd_ca_capture_destroy(capture);
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
  capture->default_listener_registered = true;
  *out = capture;
  return WD_CA_STATUS_OK;

allocation_failed:
  {
    wd_ca_set_error(error, "allocate CoreAudio capture owner");
    wd_ca_capture_destroy(capture);
    return WD_CA_STATUS_NO_MEMORY;
  }
}

static int wd_ca_prepare_session(wd_ca_capture *capture,
                                 const char *device_uid,
                                 uint32_t quantum_ms,
                                 char **error) {
  capture->quantum_ms = quantum_ms;
  atomic_store_explicit(&capture->explicit_device,
                        device_uid != NULL && device_uid[0] != '\0',
                        memory_order_release);
  wd_ca_device_info device;
  int result = wd_ca_resolve_device(device_uid, &device, error);
  if (result != WD_CA_STATUS_OK) return result;

  WDCASession *session = [[WDCASession alloc] init];
  capture->session_ref = (__bridge_retained void *)session;
  session->_tapContext = [[WDCATapContext alloc] initWithCapture:capture];
  if (session->_tapContext == nil) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "allocate CoreAudio tap context");
    return WD_CA_STATUS_START_FAILED;
  }
  session->_engine = [[AVAudioEngine alloc] init];
  session->_inputNode = session->_engine.inputNode;
  AudioUnit unit = session->_inputNode.audioUnit;
  if (unit == NULL) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "obtain CoreAudio input audio unit");
    return WD_CA_STATUS_START_FAILED;
  }
  OSStatus status = AudioUnitSetProperty(unit,
                                         kAudioOutputUnitProperty_CurrentDevice,
                                         kAudioUnitScope_Global,
                                         0,
                                         &device.id,
                                         sizeof(device.id));
  if (status != noErr) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "select CoreAudio input device: OSStatus %d", (int)status);
    return WD_CA_STATUS_START_FAILED;
  }
  AudioDeviceID effective = kAudioObjectUnknown;
  UInt32 effective_size = sizeof(effective);
  status = AudioUnitGetProperty(unit,
                                kAudioOutputUnitProperty_CurrentDevice,
                                kAudioUnitScope_Global,
                                0,
                                &effective,
                                &effective_size);
  if (status != noErr || effective != device.id) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error,
                    "verify CoreAudio input device: requested %u, effective %u, OSStatus %d",
                    device.id,
                    effective,
                    (int)status);
    return WD_CA_STATUS_START_FAILED;
  }
  wd_ca_set_device_strings(capture, device.id, device.uid, device.name);

  session->_nodeInputFormat = [session->_inputNode inputFormatForBus:0];
  session->_tapFormat = [session->_inputNode outputFormatForBus:0];
  const AudioStreamBasicDescription *description = session->_tapFormat.streamDescription;
  double tap_rate = session->_tapFormat.sampleRate;
  AVAudioChannelCount channels = session->_tapFormat.channelCount;
  if (description == NULL || description->mFormatID != kAudioFormatLinearPCM ||
      description->mBytesPerFrame == 0 || !isfinite(tap_rate) || tap_rate <= 0.0 ||
      channels == 0 || channels > WD_CA_MAX_BUFFERS) {
    char input_description[160];
    char output_description[160];
    wd_ca_describe_format(session->_nodeInputFormat, input_description, sizeof(input_description));
    wd_ca_describe_format(session->_tapFormat, output_description, sizeof(output_description));
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error,
                    "unsupported CoreAudio input-node output format; input={%s} output={%s}",
                    input_description,
                    output_description);
    return WD_CA_STATUS_START_FAILED;
  }

  double tap_ms = quantum_ms;
  if (tap_ms < WD_CA_MIN_TAP_MS) tap_ms = WD_CA_MIN_TAP_MS;
  if (tap_ms > WD_CA_MAX_TAP_MS) tap_ms = WD_CA_MAX_TAP_MS;
  double requested_frames = round(tap_rate * tap_ms / 1000.0);
  if (!isfinite(requested_frames) || requested_frames > UINT32_MAX) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "CoreAudio tap capacity overflow");
    return WD_CA_STATUS_START_FAILED;
  }
  if (requested_frames < 128.0) requested_frames = 128.0;
  capture->tap_frames = (uint32_t)requested_frames;

  bool noninterleaved = (description->mFormatFlags & kAudioFormatFlagIsNonInterleaved) != 0;
  uint32_t buffer_count = noninterleaved ? channels : 1;
  uint32_t bytes_per_frame[WD_CA_MAX_BUFFERS] = {0};
  uint32_t channels_per_buffer[WD_CA_MAX_BUFFERS] = {0};
  for (uint32_t i = 0; i < buffer_count; i++) {
    bytes_per_frame[i] = description->mBytesPerFrame;
    channels_per_buffer[i] = noninterleaved ? 1 : channels;
  }
  capture->input_buffers = buffer_count;

  double slots_value = ceil(tap_rate * 2.0 / (double)capture->tap_frames);
  if (!isfinite(slots_value) || slots_value < 1.0 || slots_value > (double)SIZE_MAX ||
      !wd_ca_raw_ring_init(&capture->raw,
                           (size_t)slots_value,
                           buffer_count,
                           bytes_per_frame,
                           channels_per_buffer,
                           capture->tap_frames,
                           WD_CA_MAX_SLOT_BYTES,
                           WD_CA_MAX_RAW_BYTES)) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "allocate two-second CoreAudio raw ring within safety limits");
    return WD_CA_STATUS_START_FAILED;
  }
  if (!atomic_is_lock_free(&capture->raw.read_index) ||
      !atomic_is_lock_free(&capture->raw.write_index) ||
      !atomic_is_lock_free(&capture->overruns) ||
      !atomic_is_lock_free(&capture->generation) ||
      !atomic_is_lock_free(&capture->tap_callbacks) ||
      !atomic_is_lock_free(&capture->tap_frames_seen) ||
      !atomic_is_lock_free(&capture->raw_buffers) ||
      !atomic_is_lock_free(&capture->raw_frames) ||
      !atomic_is_lock_free(&capture->tap_max_frames) ||
      !atomic_is_lock_free(&capture->tap_last_buffer_count) ||
      !atomic_is_lock_free(&capture->tap_failure) ||
      !atomic_is_lock_free(&session->_tapContext->_capture) ||
      !atomic_is_lock_free(&session->_tapContext->_closing) ||
      !atomic_is_lock_free(&session->_tapContext->_callbacks) ||
      !atomic_is_lock_free(&capture->state) ||
      !atomic_is_lock_free(&capture->start_phase) ||
      !atomic_is_lock_free(&capture->terminal_status) ||
      !atomic_is_lock_free(&capture->semaphore_created)) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "CoreAudio real-time indices are not lock-free on this architecture");
    return WD_CA_STATUS_START_FAILED;
  }

  session->_inputScratch = [[AVAudioPCMBuffer alloc] initWithPCMFormat:session->_tapFormat
                                                         frameCapacity:capture->tap_frames];
  session->_inputScratch.frameLength = capture->tap_frames;
  AudioBufferList *scratch_buffers = session->_inputScratch.mutableAudioBufferList;
  if (session->_inputScratch == nil || scratch_buffers == NULL ||
      scratch_buffers->mNumberBuffers != buffer_count) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "allocate CoreAudio input scratch buffer");
    return WD_CA_STATUS_START_FAILED;
  }
  for (uint32_t i = 0; i < buffer_count; i++) {
    size_t expected;
    if (!wd_ca_checked_mul(bytes_per_frame[i], capture->tap_frames, &expected) ||
        expected > scratch_buffers->mBuffers[i].mDataByteSize ||
        scratch_buffers->mBuffers[i].mNumberChannels != channels_per_buffer[i]) {
      wd_ca_device_info_destroy(&device);
      wd_ca_set_error(error, "CoreAudio input buffer layout disagrees with its stream format");
      return WD_CA_STATUS_START_FAILED;
    }
  }
  session->_inputScratch.frameLength = 0;

  session->_converterFormat = [[AVAudioFormat alloc] initStandardFormatWithSampleRate:capture->sample_rate
                                                                             channels:1];
  session->_converter = [[AVAudioConverter alloc] initFromFormat:session->_tapFormat
                                                        toFormat:session->_converterFormat];
  double output_value = ceil((double)capture->tap_frames * capture->sample_rate / tap_rate) + 256.0;
  if (session->_converter == nil || !isfinite(output_value) || output_value > UINT32_MAX) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "create CoreAudio 16 kHz mono converter");
    return WD_CA_STATUS_START_FAILED;
  }
  session->_converter.primeMethod = AVAudioConverterPrimeMethod_None;
  session->_converter.downmix = channels > 1;
  uint32_t output_capacity = (uint32_t)output_value;
  if (output_capacity < 128) output_capacity = 128;
  session->_outputScratch = [[AVAudioPCMBuffer alloc] initWithPCMFormat:session->_converterFormat
                                                          frameCapacity:output_capacity];
  if (session->_outputScratch == nil) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "allocate CoreAudio output scratch buffer");
    return WD_CA_STATUS_START_FAILED;
  }
  wd_ca_capture *block_capture = capture;
  session->_inputBlock = ^AVAudioBuffer *(AVAudioPacketCount requested, AVAudioConverterInputStatus *input_status) {
    (void)requested;
    WDCASession *active = (__bridge WDCASession *)block_capture->session_ref;
    if (active != nil && !active->_inputProvided) {
      active->_inputProvided = YES;
      *input_status = AVAudioConverterInputStatus_HaveData;
      return active->_inputScratch;
    }
    *input_status = AVAudioConverterInputStatus_NoDataNow;
    return nil;
  };

  if (semaphore_create(mach_task_self(), &capture->raw_semaphore, SYNC_POLICY_FIFO, 0) != KERN_SUCCESS) {
    wd_ca_device_info_destroy(&device);
    wd_ca_set_error(error, "create CoreAudio conversion semaphore");
    return WD_CA_STATUS_START_FAILED;
  }
  atomic_store_explicit(&capture->semaphore_created, true, memory_order_release);
  result = wd_ca_install_device_listeners(capture, device.id, error);
  wd_ca_device_info_destroy(&device);
  return result;
}

static wd_ca_capture *wd_ca_tap_enter(__unsafe_unretained WDCATapContext *context) {
  atomic_fetch_add_explicit(&context->_callbacks, 1, memory_order_seq_cst);
  wd_ca_capture *capture = atomic_load_explicit(&context->_capture, memory_order_seq_cst);
  if (capture == NULL || atomic_load_explicit(&context->_closing, memory_order_seq_cst)) {
    atomic_fetch_sub_explicit(&context->_callbacks, 1, memory_order_seq_cst);
    return NULL;
  }
  return capture;
}

static void wd_ca_tap_leave(__unsafe_unretained WDCATapContext *context) {
  atomic_fetch_sub_explicit(&context->_callbacks, 1, memory_order_seq_cst);
}

static void wd_ca_record_tap_failure(wd_ca_capture *capture, wd_ca_tap_failure failure) {
  int expected = WD_CA_TAP_FAILURE_NONE;
  atomic_compare_exchange_strong_explicit(&capture->tap_failure,
                                          &expected,
                                          failure,
                                          memory_order_relaxed,
                                          memory_order_relaxed);
}

static void wd_ca_store_tap_format_change(wd_ca_capture *capture) {
  if (wd_ca_note_startup_format_change(capture)) {
    if (atomic_load_explicit(&capture->semaphore_created, memory_order_acquire)) {
      semaphore_signal(capture->raw_semaphore);
    }
    return;
  }
  (void)wd_ca_store_terminal(capture, WD_CA_STATUS_FORMAT_CHANGED);
}

static void wd_ca_tap_copy(__unsafe_unretained WDCATapContext *context,
                           uint64_t generation,
                           __unsafe_unretained AVAudioPCMBuffer *buffer) {
  wd_ca_capture *capture = wd_ca_tap_enter(context);
  if (capture == NULL) return;
  AVAudioFrameCount frames = buffer.frameLength;
  const AudioBufferList *buffers = buffer.audioBufferList;
  atomic_fetch_add_explicit(&capture->tap_callbacks, 1, memory_order_relaxed);
  atomic_fetch_add_explicit(&capture->tap_frames_seen, frames, memory_order_relaxed);
  atomic_store_explicit(&capture->tap_last_buffer_count,
                        buffers == NULL ? 0 : buffers->mNumberBuffers,
                        memory_order_relaxed);
  uint32_t maximum = atomic_load_explicit(&capture->tap_max_frames, memory_order_relaxed);
  while (frames > maximum &&
         !atomic_compare_exchange_weak_explicit(&capture->tap_max_frames,
                                                &maximum,
                                                frames,
                                                memory_order_relaxed,
                                                memory_order_relaxed)) {
  }
  if (generation != atomic_load_explicit(&capture->generation, memory_order_relaxed)) {
    wd_ca_record_tap_failure(capture, WD_CA_TAP_FAILURE_GENERATION);
    (void)wd_ca_store_terminal(capture, WD_CA_STATUS_DEVICE_CHANGED);
    goto done;
  }
  if (frames == 0) goto done;
  if (frames > capture->tap_frames) {
    wd_ca_record_tap_failure(capture, WD_CA_TAP_FAILURE_FRAME_CAPACITY);
    wd_ca_store_tap_format_change(capture);
    goto done;
  }
  if (buffers == NULL) {
    wd_ca_record_tap_failure(capture, WD_CA_TAP_FAILURE_BUFFER_LIST);
    wd_ca_store_tap_format_change(capture);
    goto done;
  }
  if (buffers->mNumberBuffers != capture->raw.buffer_count) {
    wd_ca_record_tap_failure(capture, WD_CA_TAP_FAILURE_BUFFER_COUNT);
    wd_ca_store_tap_format_change(capture);
    goto done;
  }
  wd_ca_raw_slot *slot = NULL;
  uint64_t sequence = 0;
  if (!wd_ca_raw_ring_reserve(&capture->raw, &slot, &sequence)) {
    atomic_fetch_add_explicit(&capture->overruns, 1, memory_order_relaxed);
    goto done;
  }
  slot->frame_count = frames;
  slot->buffer_count = buffers->mNumberBuffers;
  for (uint32_t i = 0; i < buffers->mNumberBuffers; i++) {
    size_t expected;
    const AudioBuffer *source = &buffers->mBuffers[i];
    if (!wd_ca_checked_mul(capture->raw.bytes_per_frame[i], frames, &expected) ||
        expected > UINT32_MAX || source->mNumberChannels != capture->raw.channels_per_buffer[i] ||
        source->mDataByteSize != expected || source->mData == NULL) {
      wd_ca_record_tap_failure(capture, WD_CA_TAP_FAILURE_BUFFER_LAYOUT);
      wd_ca_store_tap_format_change(capture);
      goto done;
    }
    slot->byte_counts[i] = (uint32_t)expected;
    memcpy(wd_ca_raw_slot_buffer(&capture->raw, slot, i), source->mData, expected);
  }
  wd_ca_raw_ring_publish(&capture->raw, sequence);
  atomic_fetch_add_explicit(&capture->raw_buffers, 1, memory_order_relaxed);
  atomic_fetch_add_explicit(&capture->raw_frames, frames, memory_order_relaxed);
  semaphore_signal(capture->raw_semaphore);

done:
  wd_ca_tap_leave(context);
}

static int wd_ca_cleanup_failed_start(wd_ca_capture *capture,
                                      int original_result,
                                      char **error) {
  int cleanup_result = WD_CA_STATUS_OK;
  char *cleanup_error = NULL;
  if (capture->teardown_thread_started) {
    cleanup_result = wd_ca_request_teardown(capture,
                                            false,
                                            WD_CA_TEARDOWN_TIMEOUT_MS,
                                            &cleanup_error);
  } else {
    wd_ca_dispose_unstarted_session(capture);
    cleanup_result = capture->teardown_result;
    atomic_store_explicit(&capture->state, WD_CA_STATE_IDLE, memory_order_release);
  }
  if (cleanup_result != WD_CA_STATUS_OK) {
    if (cleanup_error == NULL) {
      wd_ca_set_error(&cleanup_error,
                      "%s",
                      capture->teardown_detail[0] == '\0'
                        ? "CoreAudio partial-start cleanup failed"
                        : capture->teardown_detail);
    }
    if (error != NULL) {
      free(*error);
      *error = cleanup_error;
      cleanup_error = NULL;
    }
    free(cleanup_error);
    return cleanup_result;
  }
  free(cleanup_error);
  atomic_store_explicit(&capture->terminal_status, WD_CA_STATUS_OK, memory_order_release);
  return original_result;
}

int wd_ca_capture_start(wd_ca_capture *capture,
                        const char *device_uid,
                        uint32_t quantum_ms,
                        uint32_t timeout_ms,
                        char **error) {
  if (capture == NULL || quantum_ms == 0 || timeout_ms == 0) return WD_CA_STATUS_INVALID;
  int state = atomic_load_explicit(&capture->state, memory_order_acquire);
  if (state == WD_CA_STATE_STOPPED ||
      atomic_load_explicit(&capture->stop_requested, memory_order_acquire)) {
    return WD_CA_STATUS_STOPPED;
  }
  if (state == WD_CA_STATE_CAPTURING) return WD_CA_STATUS_OK;
  if (state != WD_CA_STATE_IDLE) {
    wd_ca_set_error(error, "CoreAudio capture lifecycle transition is in progress");
    return WD_CA_STATUS_START_FAILED;
  }
  @try {
    @autoreleasepool {
      wd_ca_permission permission = WD_CA_PERMISSION_UNKNOWN;
      int result = wd_ca_permission_state(&permission, error);
      if (result != WD_CA_STATUS_OK) return result;
      if (permission != WD_CA_PERMISSION_GRANTED) {
        wd_ca_set_error(error, "microphone permission is %s",
                        permission == WD_CA_PERMISSION_NOT_DETERMINED ? "not determined" :
                        permission == WD_CA_PERMISSION_RESTRICTED ? "restricted" : "denied");
        return WD_CA_STATUS_PERMISSION;
      }

      wd_ca_reset_session(capture);
      struct timespec deadline = wd_ca_deadline(timeout_ms);
      for (;;) {
        WDCASession *session = nil;
        WDCATapContext *tap_context = nil;
        NSError *start_error = nil;
        atomic_store_explicit(&capture->conversion_exit, false, memory_order_relaxed);
        atomic_store_explicit(&capture->terminal_status, WD_CA_STATUS_OK, memory_order_relaxed);
        atomic_store_explicit(&capture->start_phase, WD_CA_START_PENDING, memory_order_relaxed);
        atomic_store_explicit(&capture->state, WD_CA_STATE_STARTING, memory_order_release);
        result = wd_ca_prepare_session(capture, device_uid, quantum_ms, error);
        session = capture->session_ref == NULL ? nil : (__bridge WDCASession *)capture->session_ref;
        if (result != WD_CA_STATUS_OK) {
          if (atomic_load_explicit(&capture->start_phase, memory_order_acquire) == WD_CA_START_REARM) {
            wd_ca_log_diagnostics(capture, session, "start", "format-rearm-prepare", result, timeout_ms);
            wd_ca_dispose_unstarted_session(capture);
            atomic_store_explicit(&capture->state, WD_CA_STATE_IDLE, memory_order_release);
            if (error != NULL) {
              free(*error);
              *error = NULL;
            }
            if (wd_ca_deadline_passed(&deadline)) {
              wd_ca_set_error(error, "CoreAudio input format did not settle within %u ms", timeout_ms);
              return WD_CA_STATUS_START_TIMEOUT;
            }
            continue;
          }
          wd_ca_append_start_diagnostics(capture,
                                         session,
                                         "prepare-failed",
                                         result,
                                         timeout_ms,
                                         error);
          return wd_ca_cleanup_failed_start(capture, result, error);
        }
        wd_ca_log_diagnostics(capture, session, "start", "prepared", WD_CA_STATUS_OK, timeout_ms);
        result = wd_ca_start_teardown_thread(capture, error);
        if (result != WD_CA_STATUS_OK) {
          wd_ca_append_start_diagnostics(capture,
                                         session,
                                         "teardown-thread-failed",
                                         result,
                                         timeout_ms,
                                         error);
          return wd_ca_cleanup_failed_start(capture, result, error);
        }
        uint64_t generation = atomic_fetch_add_explicit(&capture->generation, 1, memory_order_acq_rel) + 1;
        if (pthread_create(&capture->conversion_thread, NULL, wd_ca_conversion_main, capture) != 0) {
          wd_ca_set_error(error, "start CoreAudio conversion thread");
          result = WD_CA_STATUS_START_FAILED;
          wd_ca_append_start_diagnostics(capture,
                                         session,
                                         "conversion-thread-failed",
                                         result,
                                         timeout_ms,
                                         error);
          return wd_ca_cleanup_failed_start(capture, result, error);
        }
        capture->conversion_thread_started = true;

        tap_context = session->_tapContext;
        [session->_inputNode installTapOnBus:0
                                  bufferSize:capture->tap_frames
                                       format:session->_tapFormat
                                       block:^(__unsafe_unretained AVAudioPCMBuffer *buffer,
                                               __unsafe_unretained AVAudioTime *when) {
          (void)when;
          wd_ca_tap_copy(tap_context, generation, buffer);
        }];
        session->_tapInstalled = YES;
        [session->_engine prepare];
        if (![session->_engine startAndReturnError:&start_error]) {
          if (atomic_load_explicit(&capture->start_phase, memory_order_acquire) == WD_CA_START_REARM) {
            wd_ca_log_diagnostics(capture, session, "start", "format-rearm-engine", result, timeout_ms);
            result = wd_ca_request_teardown(capture, false, WD_CA_TEARDOWN_TIMEOUT_MS, error);
            if (result != WD_CA_STATUS_OK) return result;
            if (wd_ca_deadline_passed(&deadline)) {
              wd_ca_set_error(error, "CoreAudio input format did not settle within %u ms", timeout_ms);
              return WD_CA_STATUS_START_TIMEOUT;
            }
            continue;
          }
          wd_ca_set_nserror(error, @"start CoreAudio engine", start_error);
          result = WD_CA_STATUS_START_FAILED;
          wd_ca_append_start_diagnostics(capture,
                                         session,
                                         "engine-start-failed",
                                         result,
                                         timeout_ms,
                                         error);
          return wd_ca_cleanup_failed_start(capture, result, error);
        }
        capture->input_latency_seconds = session->_inputNode.presentationLatency;
        wd_ca_log_diagnostics(capture,
                              session,
                              "start",
                              "engine-running",
                              WD_CA_STATUS_OK,
                              timeout_ms);

        pthread_mutex_lock(&capture->wait_mutex);
        int phase = atomic_load_explicit(&capture->start_phase, memory_order_acquire);
        while (phase == WD_CA_START_PENDING &&
               atomic_load_explicit(&capture->terminal_status, memory_order_acquire) == WD_CA_STATUS_OK) {
          if (pthread_cond_timedwait(&capture->start_cond, &capture->wait_mutex, &deadline) == ETIMEDOUT) break;
          phase = atomic_load_explicit(&capture->start_phase, memory_order_acquire);
        }
        phase = atomic_load_explicit(&capture->start_phase, memory_order_acquire);
        int terminal = atomic_load_explicit(&capture->terminal_status, memory_order_acquire);
        pthread_mutex_unlock(&capture->wait_mutex);
        if (phase == WD_CA_START_REARM &&
            (terminal == WD_CA_STATUS_OK || terminal == WD_CA_STATUS_FORMAT_CHANGED ||
             terminal == WD_CA_STATUS_CONVERTER_FAILED)) {
          wd_ca_log_diagnostics(capture, session, "start", "format-rearm", terminal, timeout_ms);
          result = wd_ca_request_teardown(capture, false, WD_CA_TEARDOWN_TIMEOUT_MS, error);
          if (result != WD_CA_STATUS_OK) return result;
          if (wd_ca_deadline_passed(&deadline)) {
            wd_ca_set_error(error, "CoreAudio input format did not settle within %u ms", timeout_ms);
            return WD_CA_STATUS_START_TIMEOUT;
          }
          continue;
        }
        if (terminal != WD_CA_STATUS_OK) {
          result = terminal;
          wd_ca_set_error(error, "CoreAudio capture terminated while starting (status %d)", terminal);
          char branch[64];
          snprintf(branch, sizeof(branch), "terminal-status-%d", terminal);
          wd_ca_append_start_diagnostics(capture, session, branch, result, timeout_ms, error);
          return wd_ca_cleanup_failed_start(capture, result, error);
        }
        if (phase != WD_CA_START_CONFIRMED) {
          result = WD_CA_STATUS_START_TIMEOUT;
          wd_ca_set_error(error, "CoreAudio capture did not produce audio within %u ms", timeout_ms);
          wd_ca_append_start_diagnostics(capture, session, "timeout", result, timeout_ms, error);
          return wd_ca_cleanup_failed_start(capture, result, error);
        }
        atomic_store_explicit(&capture->state, WD_CA_STATE_CAPTURING, memory_order_release);
        wd_ca_log_diagnostics(capture,
                              session,
                              "start",
                              "confirmed",
                              WD_CA_STATUS_OK,
                              timeout_ms);
        return WD_CA_STATUS_OK;
      }
    }
  } @catch (NSException *exception) {
    wd_ca_set_error(error, "start CoreAudio capture: %s", exception.reason.UTF8String ?: "Objective-C exception");
    WDCASession *session = capture->session_ref == NULL
      ? nil
      : (__bridge WDCASession *)capture->session_ref;
    wd_ca_append_start_diagnostics(capture,
                                   session,
                                   "exception",
                                   WD_CA_STATUS_START_FAILED,
                                   timeout_ms,
                                   error);
    return wd_ca_cleanup_failed_start(capture, WD_CA_STATUS_START_FAILED, error);
  }
}

int wd_ca_capture_pause(wd_ca_capture *capture, char **error) {
  if (capture == NULL) return WD_CA_STATUS_INVALID;
  int state = atomic_load_explicit(&capture->state, memory_order_acquire);
  if (state == WD_CA_STATE_STOPPED || state == WD_CA_STATE_IDLE) return WD_CA_STATUS_OK;
  if (state == WD_CA_STATE_STARTING) {
    wd_ca_set_error(error, "CoreAudio capture is still starting");
    return WD_CA_STATUS_START_FAILED;
  }
  atomic_fetch_add_explicit(&capture->generation, 1, memory_order_acq_rel);
  return wd_ca_request_teardown(capture, false, WD_CA_TEARDOWN_TIMEOUT_MS, error);
}

static void wd_ca_unregister_listeners(wd_ca_capture *capture) {
  wd_ca_remove_device_listeners(capture);
  if (capture->default_listener_registered) {
    AudioObjectPropertyAddress address = {
      kAudioHardwarePropertyDefaultInputDevice,
      kAudioObjectPropertyScopeGlobal,
      kAudioObjectPropertyElementMain,
    };
    (void)AudioObjectRemovePropertyListener(kAudioObjectSystemObject,
                                            &address,
                                            wd_ca_property_listener,
                                            capture);
    capture->default_listener_registered = false;
  }
}

static void wd_ca_mark_stopped(wd_ca_capture *capture) {
  wd_ca_unregister_listeners(capture);
  atomic_store_explicit(&capture->terminal_status, WD_CA_STATUS_OK, memory_order_release);
  atomic_store_explicit(&capture->state, WD_CA_STATE_STOPPED, memory_order_release);
  wd_ca_wake_readers(capture);
  pthread_mutex_lock(&capture->event_mutex);
  pthread_cond_broadcast(&capture->event_cond);
  pthread_mutex_unlock(&capture->event_mutex);
}

int wd_ca_capture_stop(wd_ca_capture *capture, char **error) {
  if (capture == NULL) return WD_CA_STATUS_INVALID;
  int state = atomic_load_explicit(&capture->state, memory_order_acquire);
  if (state == WD_CA_STATE_STOPPED) return WD_CA_STATUS_OK;
  atomic_store_explicit(&capture->stop_requested, true, memory_order_release);
  if (state == WD_CA_STATE_IDLE) {
    wd_ca_mark_stopped(capture);
    return WD_CA_STATUS_OK;
  }
  int result = wd_ca_request_teardown(capture, true, WD_CA_TEARDOWN_TIMEOUT_MS, error);
  if (result == WD_CA_STATUS_OK) wd_ca_mark_stopped(capture);
  return result;
}

int wd_ca_capture_read(wd_ca_capture *capture,
                       float *samples,
                       size_t capacity,
                       uint32_t timeout_ms,
                       size_t *count,
                       char **error) {
  if (capture == NULL || samples == NULL || count == NULL) return WD_CA_STATUS_INVALID;
  *count = 0;
  if (capacity == 0) return WD_CA_STATUS_OK;
  if (timeout_ms > 20) timeout_ms = 20;

  pthread_mutex_lock(&capture->wait_mutex);
  struct timespec deadline = wd_ca_deadline(timeout_ms);
  for (;;) {
    int terminal = atomic_load_explicit(&capture->terminal_status, memory_order_acquire);
    if (terminal != WD_CA_STATUS_OK) {
      pthread_mutex_unlock(&capture->wait_mutex);
      wd_ca_set_error(error, "CoreAudio capture terminal status %d", terminal);
      return terminal;
    }
    if (atomic_load_explicit(&capture->state, memory_order_acquire) == WD_CA_STATE_STOPPED) {
      pthread_mutex_unlock(&capture->wait_mutex);
      wd_ca_set_error(error, "CoreAudio capture is stopped");
      return WD_CA_STATUS_STOPPED;
    }
    if (wd_ca_float_ring_available(&capture->processed) != 0) break;
    if (timeout_ms == 0 ||
        pthread_cond_timedwait(&capture->wait_cond, &capture->wait_mutex, &deadline) == ETIMEDOUT) {
      pthread_mutex_unlock(&capture->wait_mutex);
      return WD_CA_STATUS_OK;
    }
  }
  *count = wd_ca_float_ring_read(&capture->processed, samples, capacity);
  pthread_mutex_unlock(&capture->wait_mutex);
  return WD_CA_STATUS_OK;
}

int wd_ca_capture_stats(wd_ca_capture *capture, wd_ca_stats *stats, char **error) {
  if (capture == NULL || stats == NULL) return WD_CA_STATUS_INVALID;
  (void)error;
  if (!atomic_load_explicit(&capture->explicit_device, memory_order_acquire) &&
      atomic_load_explicit(&capture->state, memory_order_acquire) == WD_CA_STATE_IDLE &&
      atomic_exchange_explicit(&capture->default_device_dirty, false, memory_order_acq_rel)) {
    wd_ca_refresh_default_device(capture);
  }
  memset(stats, 0, sizeof(*stats));
  stats->sample_rate = capture->sample_rate;
  stats->level_dbfs = (double)atomic_load_explicit(&capture->level_mdb, memory_order_relaxed) / 1000.0;
  stats->overruns = atomic_load_explicit(&capture->overruns, memory_order_relaxed);
  stats->capturing = atomic_load_explicit(&capture->state, memory_order_acquire) == WD_CA_STATE_CAPTURING &&
                     atomic_load_explicit(&capture->terminal_status, memory_order_acquire) == WD_CA_STATUS_OK;
  pthread_mutex_lock(&capture->device_mutex);
  if (capture->device_uid != NULL) {
    strncpy(stats->device_uid, capture->device_uid, sizeof(stats->device_uid) - 1);
  }
  if (capture->device_name != NULL) {
    strncpy(stats->device_name, capture->device_name, sizeof(stats->device_name) - 1);
  }
  stats->input_latency_seconds = capture->input_latency_seconds;
  pthread_mutex_unlock(&capture->device_mutex);
  return WD_CA_STATUS_OK;
}

int wd_ca_capture_next_event(wd_ca_capture *capture,
                             uint32_t timeout_ms,
                             wd_ca_event *event,
                             char **error) {
  if (capture == NULL || event == NULL) return WD_CA_STATUS_INVALID;
  memset(event, 0, sizeof(*event));
  pthread_mutex_lock(&capture->event_mutex);
  struct timespec deadline = wd_ca_deadline(timeout_ms);
  while (capture->event_count == 0 && !capture->event_overflow &&
         atomic_load_explicit(&capture->state, memory_order_acquire) != WD_CA_STATE_STOPPED &&
         !atomic_load_explicit(&capture->stop_requested, memory_order_acquire)) {
    if (timeout_ms == 0 ||
        pthread_cond_timedwait(&capture->event_cond, &capture->event_mutex, &deadline) == ETIMEDOUT) {
      pthread_mutex_unlock(&capture->event_mutex);
      return WD_CA_STATUS_OK;
    }
  }
  if (capture->event_overflow) {
    capture->event_overflow = false;
    pthread_mutex_unlock(&capture->event_mutex);
    wd_ca_set_error(error, "CoreAudio event queue overflow");
    return WD_CA_STATUS_BACKEND_UNAVAILABLE;
  }
  if (capture->event_count != 0) {
    *event = capture->events[capture->event_head];
    capture->event_head = (capture->event_head + 1) % WD_CA_EVENT_CAPACITY;
    capture->event_count--;
    pthread_mutex_unlock(&capture->event_mutex);
    return WD_CA_STATUS_OK;
  }
  pthread_mutex_unlock(&capture->event_mutex);
  return WD_CA_STATUS_STOPPED;
}

static void wd_ca_finalize_destroy(wd_ca_capture *capture) {
  if (capture->sample_rate != 0) {
    wd_ca_unregister_listeners(capture);
    wd_ca_dispose_unstarted_session(capture);
  } else {
    wd_ca_raw_ring_destroy(&capture->raw);
  }
  wd_ca_float_ring_destroy(&capture->processed);
  free(capture->device_uid);
  free(capture->device_name);
  if (capture->start_cond_initialized) pthread_cond_destroy(&capture->start_cond);
  if (capture->wait_cond_initialized) pthread_cond_destroy(&capture->wait_cond);
  if (capture->wait_mutex_initialized) pthread_mutex_destroy(&capture->wait_mutex);
  if (capture->event_cond_initialized) pthread_cond_destroy(&capture->event_cond);
  if (capture->event_mutex_initialized) pthread_mutex_destroy(&capture->event_mutex);
  if (capture->device_mutex_initialized) pthread_mutex_destroy(&capture->device_mutex);
  if (capture->lifecycle_cond_initialized) pthread_cond_destroy(&capture->lifecycle_cond);
  if (capture->lifecycle_mutex_initialized) pthread_mutex_destroy(&capture->lifecycle_mutex);
  free(capture);
}

void wd_ca_capture_destroy(wd_ca_capture *capture) {
  if (capture == NULL) return;
  if (capture->sample_rate == 0 || !capture->lifecycle_mutex_initialized) {
    wd_ca_finalize_destroy(capture);
    return;
  }

  atomic_store_explicit(&capture->stop_requested, true, memory_order_release);
  atomic_store_explicit(&capture->state, WD_CA_STATE_STOPPING, memory_order_release);
  pthread_mutex_lock(&capture->lifecycle_mutex);
  if (capture->teardown_thread_started && !capture->teardown_done) {
    if (!capture->teardown_requested) wd_ca_gate_session(capture);
    capture->destroy_requested = true;
    capture->teardown_requested = true;
    if (!capture->teardown_detached) {
      capture->teardown_detached = pthread_detach(capture->teardown_thread) == 0;
    }
    pthread_cond_broadcast(&capture->lifecycle_cond);
    pthread_mutex_unlock(&capture->lifecycle_mutex);
    return;
  }
  if (capture->teardown_thread_started) {
    pthread_t thread = capture->teardown_thread;
    bool detached = capture->teardown_detached;
    capture->teardown_thread_started = false;
    pthread_mutex_unlock(&capture->lifecycle_mutex);
    if (!detached) (void)pthread_join(thread, NULL);
    wd_ca_finalize_destroy(capture);
    return;
  }
  pthread_mutex_unlock(&capture->lifecycle_mutex);
  wd_ca_finalize_destroy(capture);
}

int wd_ca_test_convert(const float *interleaved,
                       uint32_t frames,
                       uint32_t channels,
                       double sample_rate,
                       float **out,
                       uint32_t *out_frames,
                       char **error) {
  if (interleaved == NULL || frames == 0 || channels == 0 || channels > WD_CA_MAX_BUFFERS ||
      !isfinite(sample_rate) || sample_rate <= 0 || out == NULL || out_frames == NULL) {
    return WD_CA_STATUS_INVALID;
  }
  *out = NULL;
  *out_frames = 0;
  @try {
    @autoreleasepool {
      AVAudioFormat *input_format = [[AVAudioFormat alloc] initWithCommonFormat:AVAudioPCMFormatFloat32
                                                                     sampleRate:sample_rate
                                                                       channels:channels
                                                                    interleaved:YES];
      AVAudioFormat *output_format = [[AVAudioFormat alloc] initStandardFormatWithSampleRate:16000 channels:1];
      AVAudioConverter *converter = [[AVAudioConverter alloc] initFromFormat:input_format toFormat:output_format];
      AVAudioPCMBuffer *input = [[AVAudioPCMBuffer alloc] initWithPCMFormat:input_format frameCapacity:frames];
      if (converter == nil || input == nil || input.mutableAudioBufferList->mNumberBuffers != 1) {
        wd_ca_set_error(error, "create synthetic CoreAudio converter fixture");
        return WD_CA_STATUS_CONVERTER_FAILED;
      }
      converter.primeMethod = AVAudioConverterPrimeMethod_None;
      converter.downmix = channels > 1;
      input.frameLength = frames;
      size_t input_samples;
      size_t input_bytes;
      if (!wd_ca_checked_mul(frames, channels, &input_samples) ||
          !wd_ca_checked_mul(input_samples, sizeof(float), &input_bytes) ||
          input_bytes > input.mutableAudioBufferList->mBuffers[0].mDataByteSize) {
        return WD_CA_STATUS_INVALID;
      }
      memcpy(input.mutableAudioBufferList->mBuffers[0].mData, interleaved, input_bytes);
      input.mutableAudioBufferList->mBuffers[0].mDataByteSize = (UInt32)input_bytes;

      double capacity_value = ceil((double)frames * 16000.0 / sample_rate) + 512.0;
      if (!isfinite(capacity_value) || capacity_value > UINT32_MAX) return WD_CA_STATUS_INVALID;
      uint32_t capacity = (uint32_t)capacity_value;
      AVAudioPCMBuffer *output = [[AVAudioPCMBuffer alloc] initWithPCMFormat:output_format frameCapacity:capacity];
      float *result = calloc(capacity, sizeof(float));
      if (output == nil || result == NULL) {
        free(result);
        return WD_CA_STATUS_NO_MEMORY;
      }
      __block BOOL supplied = NO;
      AVAudioConverterInputBlock block = ^AVAudioBuffer *(AVAudioPacketCount requested,
                                                          AVAudioConverterInputStatus *status) {
        (void)requested;
        if (!supplied) {
          supplied = YES;
          *status = AVAudioConverterInputStatus_HaveData;
          return input;
        }
        *status = AVAudioConverterInputStatus_EndOfStream;
        return nil;
      };
      uint32_t total = 0;
      for (NSUInteger iteration = 0; iteration < 16; iteration++) {
        output.frameLength = 0;
        NSError *conversion_error = nil;
        AVAudioConverterOutputStatus status = [converter convertToBuffer:output
                                                                    error:&conversion_error
                                                       withInputFromBlock:block];
        if (conversion_error != nil || status == AVAudioConverterOutputStatus_Error) {
          free(result);
          wd_ca_set_nserror(error, @"convert synthetic CoreAudio fixture", conversion_error);
          return WD_CA_STATUS_CONVERTER_FAILED;
        }
        uint32_t produced = output.frameLength;
        if (produced > capacity - total || output.floatChannelData[0] == NULL) {
          free(result);
          return WD_CA_STATUS_CONVERTER_FAILED;
        }
        memcpy(result + total, output.floatChannelData[0], produced * sizeof(float));
        total += produced;
        if (status != AVAudioConverterOutputStatus_HaveData) break;
      }
      for (uint32_t i = 0; i < total; i++) {
        if (!isfinite(result[i])) result[i] = 0.0f;
        if (result[i] > 1.0f) result[i] = 1.0f;
        if (result[i] < -1.0f) result[i] = -1.0f;
      }
      *out = result;
      *out_frames = total;
      return WD_CA_STATUS_OK;
    }
  } @catch (NSException *exception) {
    wd_ca_set_error(error, "convert synthetic CoreAudio fixture: %s",
                    exception.reason.UTF8String ?: "Objective-C exception");
    return WD_CA_STATUS_CONVERTER_FAILED;
  }
}

int wd_ca_test_tap_gate(void) {
  @autoreleasepool {
    wd_ca_capture capture = {0};
    WDCATapContext *context = [[WDCATapContext alloc] initWithCapture:&capture];
    if (context == nil) return 1;
    if (wd_ca_tap_enter(context) != &capture) return 2;
    wd_ca_close_tap_gate(context);
    if (atomic_load_explicit(&context->_callbacks, memory_order_seq_cst) != 1) return 3;
    wd_ca_tap_leave(context);
    if (wd_ca_tap_enter(context) != NULL) return 4;
    if (atomic_load_explicit(&context->_callbacks, memory_order_seq_cst) != 0) return 5;
    return 0;
  }
}

typedef struct {
  wd_ca_capture *capture;
  _Atomic bool *start;
  bool format_change;
  bool result;
} wd_ca_start_race;

static void *wd_ca_test_start_race(void *value) {
  wd_ca_start_race *race = value;
  while (!atomic_load_explicit(race->start, memory_order_acquire)) {
    sched_yield();
  }
  race->result = race->format_change
    ? wd_ca_note_startup_format_change(race->capture)
    : wd_ca_confirm_startup(race->capture);
  return NULL;
}

int wd_ca_test_startup_format_change(void) {
  wd_ca_capture capture = {0};
  atomic_init(&capture.state, WD_CA_STATE_STARTING);
  atomic_init(&capture.start_phase, WD_CA_START_PENDING);
  atomic_init(&capture.terminal_status, WD_CA_STATUS_OK);
  atomic_init(&capture.semaphore_created, false);

  if (!wd_ca_note_startup_format_change(&capture)) return 1;
  if (atomic_load_explicit(&capture.start_phase, memory_order_acquire) != WD_CA_START_REARM) return 2;
  if (wd_ca_confirm_startup(&capture)) return 3;

  atomic_store_explicit(&capture.start_phase, WD_CA_START_PENDING, memory_order_release);
  if (!wd_ca_confirm_startup(&capture)) return 4;
  if (wd_ca_note_startup_format_change(&capture)) return 5;

  for (NSUInteger iteration = 0; iteration < 1000; iteration++) {
    atomic_store_explicit(&capture.start_phase, WD_CA_START_PENDING, memory_order_release);
    _Atomic bool start;
    atomic_init(&start, false);
    wd_ca_start_race format = {
      .capture = &capture,
      .start = &start,
      .format_change = true,
    };
    wd_ca_start_race confirm = {
      .capture = &capture,
      .start = &start,
      .format_change = false,
    };
    pthread_t format_thread;
    pthread_t confirm_thread;
    if (pthread_create(&format_thread, NULL, wd_ca_test_start_race, &format) != 0) return 6;
    if (pthread_create(&confirm_thread, NULL, wd_ca_test_start_race, &confirm) != 0) {
      atomic_store_explicit(&start, true, memory_order_release);
      (void)pthread_join(format_thread, NULL);
      return 7;
    }
    atomic_store_explicit(&start, true, memory_order_release);
    (void)pthread_join(format_thread, NULL);
    (void)pthread_join(confirm_thread, NULL);
    int phase = atomic_load_explicit(&capture.start_phase, memory_order_acquire);
    if (format.result == confirm.result) return 8;
    if (format.result && phase != WD_CA_START_REARM) return 9;
    if (confirm.result && phase != WD_CA_START_CONFIRMED) return 10;
  }

  atomic_store_explicit(&capture.start_phase, WD_CA_START_PENDING, memory_order_release);
  wd_ca_store_tap_format_change(&capture);
  if (atomic_load_explicit(&capture.start_phase, memory_order_acquire) != WD_CA_START_REARM ||
      atomic_load_explicit(&capture.terminal_status, memory_order_acquire) != WD_CA_STATUS_OK) {
    return 11;
  }
  atomic_store_explicit(&capture.state, WD_CA_STATE_CAPTURING, memory_order_release);
  atomic_store_explicit(&capture.start_phase, WD_CA_START_CONFIRMED, memory_order_release);
  wd_ca_store_tap_format_change(&capture);
  if (atomic_load_explicit(&capture.terminal_status, memory_order_acquire) !=
      WD_CA_STATUS_FORMAT_CHANGED) {
    return 12;
  }
  return 0;
}

static void *wd_ca_test_slow_teardown(void *value) {
  wd_ca_capture *capture = value;
  pthread_mutex_lock(&capture->lifecycle_mutex);
  while (!capture->teardown_requested) {
    pthread_cond_wait(&capture->lifecycle_cond, &capture->lifecycle_mutex);
  }
  pthread_mutex_unlock(&capture->lifecycle_mutex);
  struct timespec delay = {.tv_sec = 0, .tv_nsec = 500000000L};
  while (nanosleep(&delay, &delay) != 0 && errno == EINTR) {
  }
  pthread_mutex_lock(&capture->lifecycle_mutex);
  capture->teardown_result = WD_CA_STATUS_OK;
  capture->teardown_done = true;
  pthread_cond_broadcast(&capture->lifecycle_cond);
  pthread_mutex_unlock(&capture->lifecycle_mutex);
  return NULL;
}

int wd_ca_test_teardown_timeout(uint32_t *elapsed_ms) {
  if (elapsed_ms == NULL) return 1;
  wd_ca_capture capture = {0};
  if (pthread_mutex_init(&capture.lifecycle_mutex, NULL) != 0) return 2;
  if (pthread_cond_init(&capture.lifecycle_cond, NULL) != 0) return 3;
  if (pthread_cond_init(&capture.wait_cond, NULL) != 0) return 4;
  if (pthread_cond_init(&capture.start_cond, NULL) != 0) return 5;
  if (pthread_cond_init(&capture.event_cond, NULL) != 0) return 6;
  atomic_init(&capture.state, WD_CA_STATE_CAPTURING);
  atomic_init(&capture.terminal_status, WD_CA_STATUS_OK);
  atomic_init(&capture.conversion_exit, false);
  atomic_init(&capture.semaphore_created, false);
  atomic_init(&capture.stop_requested, false);
  if (pthread_create(&capture.teardown_thread, NULL, wd_ca_test_slow_teardown, &capture) != 0) return 7;
  capture.teardown_thread_started = true;

  struct timespec started;
  struct timespec finished;
  clock_gettime(CLOCK_MONOTONIC, &started);
  int result = wd_ca_request_teardown(&capture, false, WD_CA_TEARDOWN_TIMEOUT_MS, NULL);
  clock_gettime(CLOCK_MONOTONIC, &finished);
  int64_t seconds = finished.tv_sec - started.tv_sec;
  int64_t nanoseconds = finished.tv_nsec - started.tv_nsec;
  if (nanoseconds < 0) {
    seconds--;
    nanoseconds += 1000000000L;
  }
  uint64_t elapsed = (uint64_t)seconds * 1000 + (uint64_t)nanoseconds / 1000000;
  *elapsed_ms = elapsed > UINT32_MAX ? UINT32_MAX : (uint32_t)elapsed;

  pthread_mutex_lock(&capture.lifecycle_mutex);
  while (!capture.teardown_done) {
    pthread_cond_wait(&capture.lifecycle_cond, &capture.lifecycle_mutex);
  }
  pthread_mutex_unlock(&capture.lifecycle_mutex);
  pthread_cond_destroy(&capture.event_cond);
  pthread_cond_destroy(&capture.start_cond);
  pthread_cond_destroy(&capture.wait_cond);
  pthread_cond_destroy(&capture.lifecycle_cond);
  pthread_mutex_destroy(&capture.lifecycle_mutex);
  return result == WD_CA_STATUS_TEARDOWN_TIMEOUT ? 0 : 8;
}
