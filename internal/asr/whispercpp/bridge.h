#ifndef WAYDICT_WHISPERCPP_BRIDGE_H
#define WAYDICT_WHISPERCPP_BRIDGE_H

#include <stdbool.h>
#include <whisper.h>

void waydict_whisper_log_install(void);
struct whisper_context *waydict_whisper_init(const char *model_path, bool use_gpu, int device);
int waydict_whisper_full(struct whisper_context *ctx, const float *samples, int sample_count, int thread_count, const char *initial_prompt);

#endif
