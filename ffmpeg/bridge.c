#include "_cgo_export.h"   // 由 cgo 生成，包含 go 导出函数声明

#include <libavutil/log.h>
#include <stdarg.h>
#include <string.h>

// helper
static const char* lvl_tag(int level) {
    if (level >= AV_LOG_PANIC)   return "PANIC";
    if (level >= AV_LOG_FATAL)   return "FATAL";
    if (level >= AV_LOG_ERROR)   return "ERROR";
    if (level >= AV_LOG_WARNING) return "WARN";
    if (level >= AV_LOG_INFO)    return "INFO";
    if (level >= AV_LOG_VERBOSE) return "VERB";
    if (level >= AV_LOG_DEBUG)   return "DEBUG";
    return "TRACE";
}

static void bridge_log_callback(void* ptr, int level, const char* fmt, va_list vl) {
    char buf[1024];
    vsnprintf(buf, sizeof(buf), fmt, vl);
    size_t n = strlen(buf);
    if (n && buf[n-1] == '\n') buf[n-1] = '\0';
    // _cgo_export.h declares: void goLogCallbackTagged(char* tag, char* msg);
    goLogCallbackTagged((char*)lvl_tag(level), buf);
}

// optional: a C function to install the callback so Go can call it
void enable_ffmpeg_log_callback_with_level(int level) {
    av_log_set_callback(bridge_log_callback);
    av_log_set_flags(AV_LOG_SKIP_REPEATED);
    av_log_set_level(level);
}
