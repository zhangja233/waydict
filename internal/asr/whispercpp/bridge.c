//go:build whispercpp && cgo

#include "bridge.h"

extern void waydictWhisperLog(int level, char *text);

static void waydict_whisper_log(enum ggml_log_level level, const char *text, void *user_data) {
    (void) user_data;
    waydictWhisperLog((int) level, (char *) text);
}

void waydict_whisper_log_install(void) {
    // whisper_log_set also routes ggml logs through this callback.
    whisper_log_set(waydict_whisper_log, NULL);
}

struct whisper_context *waydict_whisper_init(const char *model_path, bool use_gpu, int device) {
    struct whisper_context_params params = whisper_context_default_params();
    params.use_gpu = use_gpu;
    params.gpu_device = device;
    return whisper_init_from_file_with_params(model_path, params);
}

int waydict_whisper_full(struct whisper_context *ctx, const float *samples, int sample_count, int thread_count, const char *initial_prompt) {
    struct whisper_full_params params = whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
    params.n_threads = thread_count;
    params.no_context = true;
    params.no_timestamps = true;
    params.single_segment = true;
    params.token_timestamps = false;
    params.print_special = false;
    params.print_progress = false;
    params.print_realtime = false;
    params.print_timestamps = false;
    params.temperature = 0.0f;
    params.temperature_inc = -1.0f;
    params.language = whisper_is_multilingual(ctx) ? "en" : NULL;
    params.detect_language = false;
    params.initial_prompt = initial_prompt;
    return whisper_full(ctx, params, samples, sample_count);
}
