#include "_cgo_export.h"

#include <libavutil/log.h>
#include <stdarg.h>
#include <stdatomic.h>
#include <stdio.h>

static _Atomic int bridge_log_level = AV_LOG_WARNING;

static void bridge_log_callback(void *ptr, int level, const char *fmt, va_list vl) {
    char buf[4096];
    const char *component = "";
    level &= 0xff;
    // Custom callbacks must enforce filtering themselves. av_log() does not
    // apply av_log_get_level() before invoking a registered callback.
    if (level > atomic_load_explicit(&bridge_log_level, memory_order_relaxed))
        return;
    if (ptr) {
        const AVClass *avc = *(const AVClass **)ptr;
        if (avc && avc->item_name)
            component = avc->item_name(ptr);
    }
    vsnprintf(buf, sizeof(buf), fmt, vl);
    goLogCallback(level, (char *)(component ? component : ""), buf);
}

void install_ffmpeg_log_callback(void) {
    av_log_set_callback(bridge_log_callback);
}

void set_ffmpeg_log_level(int level) {
    atomic_store_explicit(&bridge_log_level, level, memory_order_relaxed);
    av_log_set_level(level);
}
