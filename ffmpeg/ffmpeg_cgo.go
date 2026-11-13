//go:build cgo
// +build cgo

package ffmpeg

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
	"unsafe"
)

/*
#cgo CFLAGS: -I${SRCDIR}/ffmpeg-static/include
#cgo LDFLAGS: -L${SRCDIR}/ffmpeg-static/lib

// macOS
#cgo darwin LDFLAGS: -lavfilter -lswscale -lavformat -lavcodec -lavutil
#cgo darwin,!ffmpeg_minimal LDFLAGS: -lx264
#cgo darwin LDFLAGS: -lssl -lcrypto -lm -lpthread
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security -framework VideoToolbox -framework CoreMedia -framework CoreVideo

// Linux
#cgo linux LDFLAGS: -lavfilter -lswscale -lavformat -lavcodec -lavutil
#cgo linux,!ffmpeg_minimal LDFLAGS: -lx264
#cgo linux LDFLAGS: -lssl -lcrypto -lpthread -ldl -lm

// Windows (static)
#cgo windows LDFLAGS: -static -lavfilter -lswscale -lavformat -lavcodec -lavutil
#cgo windows,!ffmpeg_minimal LDFLAGS: -lx264
#cgo windows LDFLAGS: -lssl -lcrypto -lm -lpthread
#cgo windows LDFLAGS: -lws2_32 -lwinmm -lole32 -lshlwapi -lbcrypt -lcrypt32 -lkernel32 -luser32 -ladvapi32 -lmsvcrt -lucrtbase

#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersink.h>
#include <libavfilter/buffersrc.h>
#include <libswscale/swscale.h>
#include <libavutil/opt.h>
#include <libavutil/avutil.h>
#include <libavutil/pixfmt.h>
#include <libavutil/error.h>
#include <libavutil/imgutils.h>
#include <libavutil/frame.h>
#include <libavutil/log.h>
#include <libavutil/time.h>
#include <stdarg.h>
#include <string.h>

// helpers
static inline int averror_eagain() { return AVERROR(EAGAIN); }
static inline int averror_eof()    { return AVERROR_EOF; }

static int input_deadline_exceeded(void *opaque) {
    return av_gettime_relative() >= *(int64_t *)opaque;
}

static void set_input_deadline(AVFormatContext *ctx, int64_t *deadline, int64_t timeout) {
    *deadline = av_gettime_relative() + timeout;
    ctx->interrupt_callback.callback = input_deadline_exceeded;
    ctx->interrupt_callback.opaque = deadline;
}

static int make_buffer_src(AVFilterGraph *g, AVFilterContext **src,
                           const char *name, int w, int h, enum AVPixelFormat pix,
                           AVRational time_base) {
    char args[128];
    snprintf(args, sizeof(args),
             "video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=1/1",
             w, h, pix, time_base.num, time_base.den);
    const AVFilter *buffer = avfilter_get_by_name("buffer");
    if (!buffer) return AVERROR_FILTER_NOT_FOUND;
    return avfilter_graph_create_filter(src, buffer, name, args, NULL, g);
}

static int make_buffer_sink(AVFilterGraph *g, AVFilterContext **sink,
                            const char *name) {
    const AVFilter *buffersink = avfilter_get_by_name("buffersink");
    if (!buffersink) return AVERROR_FILTER_NOT_FOUND;
    return avfilter_graph_create_filter(sink, buffersink, name, NULL, NULL, g);
}

void install_ffmpeg_log_callback(void);
void set_ffmpeg_log_level(int level);
*/
import "C"

var nativeLoggingOnce sync.Once

//export goLogCallback
func goLogCallback(level C.int, component, msg *C.char) {
	logFFmpeg(int(level), "cgo", C.GoString(component), C.GoString(msg))
}

func configureNativeLogging(level int) {
	nativeLoggingOnce.Do(func() { C.install_ffmpeg_log_callback() })
	C.set_ffmpeg_log_level(C.int(level))
}

func ConvMediaWithProxy(url, outputPath, proxyURL, mediaType string) error {
	return convMediaWithTimeout(url, outputPath, proxyURL, mediaType, mediaConversionTimeout)
}

func convMediaWithTimeout(url, outputPath, proxyURL, mediaType string, timeout time.Duration) error {
	cUrl := C.CString(url)
	defer C.free(unsafe.Pointer(cUrl))
	cOut := C.CString(outputPath)
	defer C.free(unsafe.Pointer(cOut))

	inFmt := C.avformat_alloc_context()
	if inFmt == nil {
		return fmt.Errorf("alloc input context failed")
	}
	deadline := (*C.int64_t)(C.av_malloc(C.size_t(unsafe.Sizeof(C.int64_t(0)))))
	if deadline == nil {
		C.avformat_close_input(&inFmt)
		return fmt.Errorf("alloc input deadline failed")
	}
	defer func() {
		C.avformat_close_input(&inFmt)
		C.av_free(unsafe.Pointer(deadline))
	}()
	C.set_input_deadline(inFmt, deadline, C.int64_t(timeout.Microseconds()))
	var opts *C.AVDictionary
	defer C.av_dict_free(&opts)
	setOption := func(key, value string) {
		cKey, cValue := C.CString(key), C.CString(value)
		defer C.free(unsafe.Pointer(cKey))
		defer C.free(unsafe.Pointer(cValue))
		C.av_dict_set(&opts, cKey, cValue, 0)
	}

	// Set timeouts for direct requests as well as proxied requests. The interrupt
	// callback also bounds HLS probing and the complete multi-segment download.
	setOption("rw_timeout", strconv.FormatInt(min(timeout, inputReadTimeout).Microseconds(), 10))
	if proxyURL != "" {
		setOption("http_proxy", proxyURL)
	}

	// Open input
	if ret := C.avformat_open_input(&inFmt, cUrl, nil, &opts); ret < 0 {
		return fmt.Errorf("open input failed: %s", avErr2Str(ret))
	}

	if ret := C.avformat_find_stream_info(inFmt, nil); ret < 0 {
		return fmt.Errorf("find stream info failed: %s", avErr2Str(ret))
	}

	switch mediaType {
	case "mp4":
		return copyToMP4(inFmt, cOut)
	case "gif":
		return transcodeToGIF(inFmt, cOut)
	default:
		return fmt.Errorf("unsupported type: %s", mediaType)
	}
}

func copyToMP4(inFmt *C.AVFormatContext, cOut *C.char) (resultErr error) {
	var outFmt *C.AVFormatContext
	format := C.CString("mp4")
	defer C.free(unsafe.Pointer(format))

	if ret := C.avformat_alloc_output_context2(&outFmt, nil, format, cOut); ret < 0 {
		return fmt.Errorf("alloc output context failed: %s", avErr2Str(ret))
	}
	defer C.avformat_free_context(outFmt)

	// Match the CLI's default stream selection: one video and its related audio,
	// rather than every rendition exposed by an HLS master playlist.
	videoIndex := C.av_find_best_stream(inFmt, C.AVMEDIA_TYPE_VIDEO, -1, -1, nil, 0)
	audioIndex := C.av_find_best_stream(inFmt, C.AVMEDIA_TYPE_AUDIO, -1, videoIndex, nil, 0)
	if videoIndex < 0 && audioIndex < 0 {
		return fmt.Errorf("no audio or video stream found")
	}
	streamMap := make([]int, int(inFmt.nb_streams))
	for i := 0; i < int(inFmt.nb_streams); i++ {
		inStream := getStream(inFmt, i)
		streamMap[i] = -1
		if C.int(i) != videoIndex && C.int(i) != audioIndex {
			inStream.discard = C.AVDISCARD_ALL
			continue
		}
		outStream := C.avformat_new_stream(outFmt, nil)
		if outStream == nil {
			return fmt.Errorf("failed to create new stream")
		}
		if ret := C.avcodec_parameters_copy(outStream.codecpar, inStream.codecpar); ret < 0 {
			return fmt.Errorf("copy codec parameters failed: %s", avErr2Str(ret))
		}
		// MPEG-TS codec tags from HLS are not valid MP4 sample entry tags.
		// Let the output muxer choose a tag for the copied codec instead.
		outStream.codecpar.codec_tag = 0
		outStream.time_base = inStream.time_base
		streamMap[i] = int(outStream.index)
	}

	if (outFmt.oformat.flags & C.AVFMT_NOFILE) == 0 {
		if ret := C.avio_open(&outFmt.pb, cOut, C.AVIO_FLAG_WRITE); ret < 0 {
			return fmt.Errorf("open output file failed: %s", avErr2Str(ret))
		}
		defer func() {
			C.avio_closep(&outFmt.pb)
			if resultErr != nil {
				os.Remove(C.GoString(cOut))
			}
		}()
	}

	if ret := C.avformat_write_header(outFmt, nil); ret < 0 {
		return fmt.Errorf("write header failed: %s", avErr2Str(ret))
	}

	var pkt C.AVPacket
	for {
		ret := C.av_read_frame(inFmt, &pkt)
		if ret == C.averror_eof() {
			break
		}
		if ret < 0 {
			return fmt.Errorf("read input failed: %s", avErr2Str(ret))
		}
		inStream := getStream(inFmt, int(pkt.stream_index))
		mapped := streamMap[int(pkt.stream_index)]
		if mapped < 0 {
			C.av_packet_unref(&pkt)
			continue
		}
		outStream := getStream(outFmt, mapped)

		C.av_packet_rescale_ts(&pkt, inStream.time_base, outStream.time_base)
		pkt.stream_index = C.int(mapped)
		pkt.pos = -1

		if ret := C.av_interleaved_write_frame(outFmt, &pkt); ret < 0 {
			C.av_packet_unref(&pkt)
			return fmt.Errorf("write frame failed: %s", avErr2Str(ret))
		}
		C.av_packet_unref(&pkt)
	}

	if ret := C.av_write_trailer(outFmt); ret < 0 {
		return fmt.Errorf("write trailer failed: %s", avErr2Str(ret))
	}

	fi, err := os.Stat(C.GoString(cOut))
	if err != nil {
		return fmt.Errorf("failed to check output file: %v", err)
	}
	if fi.Size() == 0 {
		os.Remove(C.GoString(cOut))
		return fmt.Errorf("generated MP4 file is empty")
	}

	return nil
}

func avErr2Str(err C.int) string {
	buf := make([]C.char, 256)
	C.av_strerror(err, (*C.char)(unsafe.Pointer(&buf[0])), 256)
	return C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
}

func getStream(fmtCtx *C.AVFormatContext, i int) *C.AVStream {
	return *(**C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(fmtCtx.streams)) + uintptr(i)*unsafe.Sizeof(fmtCtx.streams)))
}

// ---------- GIF single-pass pipeline ----------

type RGBFrame struct {
	frame *C.AVFrame
	pts   C.int64_t
}

func transcodeToGIF(inFmt *C.AVFormatContext, cOut *C.char) error {
	logFFmpeg(avLogVerbose, "cgo", "gif", "start transcode")

	// Find video stream
	var videoStream *C.AVStream
	var streamIndex C.int = -1
	for i := 0; i < int(inFmt.nb_streams); i++ {
		st := getStream(inFmt, i)
		if st.codecpar.codec_type == C.AVMEDIA_TYPE_VIDEO {
			videoStream = st
			streamIndex = C.int(i)
			break
		}
	}
	if videoStream == nil {
		return fmt.Errorf("no video stream found")
	}

	// Derive timing from input avg_frame_rate
	inNum := int(videoStream.avg_frame_rate.num)
	inDen := int(videoStream.avg_frame_rate.den)
	if inNum <= 0 || inDen <= 0 {
		inNum, inDen = 10, 1
	}
	var tbNum, tbDen C.int
	C.av_reduce(&tbNum, &tbDen, C.int64_t(inDen), C.int64_t(inNum), C.int64_t((1<<30)-1))
	var frNum, frDen C.int
	C.av_reduce(&frNum, &frDen, C.int64_t(inNum), C.int64_t(inDen), C.int64_t((1<<30)-1))
	timeBase := C.AVRational{num: tbNum, den: tbDen}
	fr := C.AVRational{num: frNum, den: frDen}

	// Output context + encoder
	var outFmt *C.AVFormatContext
	format := C.CString("gif")
	defer C.free(unsafe.Pointer(format))
	if ret := C.avformat_alloc_output_context2(&outFmt, nil, format, cOut); ret < 0 {
		return fmt.Errorf("alloc output context failed: %s", avErr2Str(ret))
	}
	defer C.avformat_free_context(outFmt)

	codec := C.avcodec_find_encoder(C.AV_CODEC_ID_GIF)
	if codec == nil {
		return fmt.Errorf("GIF encoder not found")
	}
	outStream := C.avformat_new_stream(outFmt, codec)
	if outStream == nil {
		return fmt.Errorf("failed to create output stream")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	defer C.avcodec_free_context(&codecCtx)
	codecCtx.width = videoStream.codecpar.width
	codecCtx.height = videoStream.codecpar.height
	codecCtx.pix_fmt = C.AV_PIX_FMT_PAL8
	codecCtx.time_base = timeBase
	codecCtx.framerate = fr

	if ret := C.avcodec_open2(codecCtx, codec, nil); ret < 0 {
		return fmt.Errorf("open codec failed: %s", avErr2Str(ret))
	}
	if ret := C.avcodec_parameters_from_context(outStream.codecpar, codecCtx); ret < 0 {
		return fmt.Errorf("copy codec params failed: %s", avErr2Str(ret))
	}
	outStream.time_base = timeBase
	outStream.avg_frame_rate = fr

	if (outFmt.oformat.flags & C.AVFMT_NOFILE) == 0 {
		if ret := C.avio_open(&outFmt.pb, cOut, C.AVIO_FLAG_WRITE); ret < 0 {
			return fmt.Errorf("open output file failed: %s", avErr2Str(ret))
		}
		defer C.avio_closep(&outFmt.pb)
	}
	if ret := C.avformat_write_header(outFmt, nil); ret < 0 {
		return fmt.Errorf("write header failed: %s", avErr2Str(ret))
	}
	logFFmpeg(avLogVerbose, "cgo", "gif", "header written")

	// Decoder
	dec := C.avcodec_find_decoder(videoStream.codecpar.codec_id)
	if dec == nil {
		return fmt.Errorf("video decoder not found")
	}
	decCtx := C.avcodec_alloc_context3(dec)
	if decCtx == nil {
		return fmt.Errorf("alloc decoder context failed")
	}
	defer C.avcodec_free_context(&decCtx)
	if ret := C.avcodec_parameters_to_context(decCtx, videoStream.codecpar); ret < 0 {
		return fmt.Errorf("copy decoder parameters failed: %s", avErr2Str(ret))
	}
	if ret := C.avcodec_open2(decCtx, dec, nil); ret < 0 {
		return fmt.Errorf("open decoder failed: %s", avErr2Str(ret))
	}

	frame := C.av_frame_alloc()
	defer C.av_frame_free(&frame)

	var swsYuv2Rgb *C.struct_SwsContext
	var cachedFrames []RGBFrame
	defer func() {
		for _, cf := range cachedFrames {
			C.av_frame_free(&cf.frame)
		}
	}()

	// Build single-pass filter graph
	logFFmpeg(avLogVerbose, "cgo", "gif", "build single-pass graph")
	graph := C.avfilter_graph_alloc()
	if graph == nil {
		return fmt.Errorf("avfilter_graph_alloc failed")
	}
	defer C.avfilter_graph_free(&graph)

	var srcCtx, sinkCtx *C.AVFilterContext

	cname := C.CString("src")
	ret := C.make_buffer_src(graph, &srcCtx, cname, codecCtx.width, codecCtx.height, C.AV_PIX_FMT_RGB24, timeBase)
	C.free(unsafe.Pointer(cname))
	if ret < 0 {
		return fmt.Errorf("buffer src failed: %s", avErr2Str(ret))
	}

	cname = C.CString("sink")
	ret = C.make_buffer_sink(graph, &sinkCtx, cname)
	C.free(unsafe.Pointer(cname))
	if ret < 0 {
		return fmt.Errorf("buffer sink failed: %s", avErr2Str(ret))
	}

	outs := C.avfilter_inout_alloc()
	ins := C.avfilter_inout_alloc()
	// 内层 C.CString 需手动释放，避免每次转码泄漏
	cTmp := C.CString("src")
	outs.name = C.av_strdup(cTmp)
	C.free(unsafe.Pointer(cTmp))
	outs.filter_ctx = srcCtx
	outs.pad_idx = 0
	outs.next = nil

	cTmp = C.CString("sink")
	ins.name = C.av_strdup(cTmp)
	C.free(unsafe.Pointer(cTmp))
	ins.filter_ctx = sinkCtx
	ins.pad_idx = 0
	ins.next = nil

	fpsStr := fmt.Sprintf("%d/%d", inNum, inDen)

	desc := C.CString(fmt.Sprintf(
		"[src]split[a][b];"+
			"[b]fps=%s,palettegen=max_colors=256:stats_mode=full[p];"+
			"[a][p]paletteuse=dither=floyd_steinberg,fps=%s,format=pix_fmts=pal8[sink]",
		fpsStr, fpsStr,
	))
	ret = C.avfilter_graph_parse_ptr(graph, desc, &ins, &outs, nil)
	C.free(unsafe.Pointer(desc))
	if ret < 0 {
		return fmt.Errorf("graph parse failed: %s", avErr2Str(ret))
	}
	if ret = C.avfilter_graph_config(graph, nil); ret < 0 {
		return fmt.Errorf("graph config failed: %s", avErr2Str(ret))
	}
	C.avfilter_inout_free(&ins)
	C.avfilter_inout_free(&outs)
	logFFmpeg(avLogVerbose, "cgo", "gif", "graph ready")

	// Decode → RGB → push
	var pkt C.AVPacket
	for {
		ret := C.av_read_frame(inFmt, &pkt)
		if ret == C.averror_eof() {
			break
		}
		if ret < 0 {
			return fmt.Errorf("read input failed: %s", avErr2Str(ret))
		}
		if pkt.stream_index != streamIndex {
			C.av_packet_unref(&pkt)
			continue
		}
		if s := C.avcodec_send_packet(decCtx, &pkt); s < 0 {
			C.av_packet_unref(&pkt)
			return fmt.Errorf("send packet failed: %s", avErr2Str(s))
		}
		C.av_packet_unref(&pkt)

		for {
			rr := C.avcodec_receive_frame(decCtx, frame)
			if rr == C.averror_eagain() || rr == C.averror_eof() {
				break
			}
			if rr < 0 {
				return fmt.Errorf("decode failed: %s", avErr2Str(rr))
			}

			if swsYuv2Rgb == nil {
				swsYuv2Rgb = C.sws_getContext(frame.width, frame.height, (C.enum_AVPixelFormat)(frame.format),
					codecCtx.width, codecCtx.height, C.AV_PIX_FMT_RGB24, C.SWS_BILINEAR, nil, nil, nil)
				if swsYuv2Rgb == nil {
					return fmt.Errorf("sws_getContext failed")
				}
				defer C.sws_freeContext(swsYuv2Rgb)
			}

			scratch := C.av_frame_alloc()
			scratch.format = C.AV_PIX_FMT_RGB24
			scratch.width = codecCtx.width
			scratch.height = codecCtx.height
			if ret := C.av_frame_get_buffer(scratch, 32); ret < 0 {
				C.av_frame_free(&scratch)
				return fmt.Errorf("alloc RGB scratch failed: %s", avErr2Str(ret))
			}

			C.sws_scale(swsYuv2Rgb, &frame.data[0], &frame.linesize[0], 0, frame.height,
				&scratch.data[0], &scratch.linesize[0])

			scratch.pts = C.int64_t(len(cachedFrames))
			scratch.duration = 1

			if r := C.av_buffersrc_add_frame_flags(srcCtx, scratch, 0); r < 0 {
				C.av_frame_free(&scratch)
				return fmt.Errorf("push src RGB failed: %s", avErr2Str(r))
			}

			copyF := C.av_frame_alloc()
			copyF.format = C.AV_PIX_FMT_RGB24
			copyF.width = codecCtx.width
			copyF.height = codecCtx.height
			if ret := C.av_frame_get_buffer(copyF, 32); ret < 0 {
				C.av_frame_free(&copyF)
				C.av_frame_free(&scratch)
				return fmt.Errorf("alloc copyF failed: %s", avErr2Str(ret))
			}
			C.av_frame_copy_props(copyF, scratch)
			copyF.pts = scratch.pts
			copyF.duration = 1
			C.av_image_copy(&copyF.data[0], &copyF.linesize[0],
				(**C.uint8_t)(unsafe.Pointer(&scratch.data[0])),
				&scratch.linesize[0],
				C.AV_PIX_FMT_RGB24, codecCtx.width, codecCtx.height)
			cachedFrames = append(cachedFrames, RGBFrame{frame: copyF, pts: copyF.pts})

			C.av_frame_free(&scratch)
			C.av_frame_unref(frame)
		}
	}

	// Flush decoder & graph
	C.avcodec_send_packet(decCtx, nil)
	for {
		rr := C.avcodec_receive_frame(decCtx, frame)
		if rr == C.averror_eagain() || rr == C.averror_eof() {
			break
		}
		if rr < 0 {
			return fmt.Errorf("decode flush failed: %s", avErr2Str(rr))
		}

		scratch := C.av_frame_alloc()
		scratch.format = C.AV_PIX_FMT_RGB24
		scratch.width = codecCtx.width
		scratch.height = codecCtx.height
		if ret := C.av_frame_get_buffer(scratch, 32); ret < 0 {
			C.av_frame_free(&scratch)
			return fmt.Errorf("alloc RGB scratch (flush) failed: %s", avErr2Str(ret))
		}
		C.sws_scale(swsYuv2Rgb, &frame.data[0], &frame.linesize[0], 0, frame.height,
			&scratch.data[0], &scratch.linesize[0])

		scratch.pts = C.int64_t(len(cachedFrames))
		scratch.duration = 1

		if r := C.av_buffersrc_add_frame_flags(srcCtx, scratch, 0); r < 0 {
			C.av_frame_free(&scratch)
			return fmt.Errorf("push src RGB (flush) failed: %s", avErr2Str(r))
		}

		copyF := C.av_frame_alloc()
		copyF.format = C.AV_PIX_FMT_RGB24
		copyF.width = codecCtx.width
		copyF.height = codecCtx.height
		if ret := C.av_frame_get_buffer(copyF, 32); ret < 0 {
			C.av_frame_free(&copyF)
			C.av_frame_free(&scratch)
			return fmt.Errorf("alloc copyF (flush) failed: %s", avErr2Str(ret))
		}
		C.av_frame_copy_props(copyF, scratch)
		copyF.pts = scratch.pts
		copyF.duration = 1
		C.av_image_copy(&copyF.data[0], &copyF.linesize[0],
			(**C.uint8_t)(unsafe.Pointer(&scratch.data[0])),
			&scratch.linesize[0],
			C.AV_PIX_FMT_RGB24, codecCtx.width, codecCtx.height)
		cachedFrames = append(cachedFrames, RGBFrame{frame: copyF, pts: copyF.pts})

		C.av_frame_free(&scratch)
		C.av_frame_unref(frame)
	}
	C.av_buffersrc_add_frame_flags(srcCtx, nil, 0)
	logFFmpeg(avLogVerbose, "cgo", "gif", "source flushed; drain and encode")

	// Drain sink and encode
	var passFrames, passPkts int
	for {
		out := C.av_frame_alloc()
		r := C.av_buffersink_get_frame(sinkCtx, out)
		if r == C.averror_eagain() || r == C.AVERROR_EOF {
			C.av_frame_free(&out)
			break
		}
		if r < 0 {
			C.av_frame_free(&out)
			return fmt.Errorf("sink get failed: %s", avErr2Str(r))
		}

		if out.pts == C.int64_t(C.AV_NOPTS_VALUE) {
			out.pts = C.int64_t(passFrames)
		}
		if out.duration == 0 {
			out.duration = 1
		}
		out.pict_type = C.AV_PICTURE_TYPE_NONE

		passFrames++
		logFFmpeg(avLogTrace, "cgo", "gif", fmt.Sprintf("PAL8 frame pts=%d", int64(out.pts)))

		if er := C.avcodec_send_frame(codecCtx, out); er < 0 {
			C.av_frame_free(&out)
			return fmt.Errorf("encode send failed: %s", avErr2Str(er))
		}
		for {
			var encPkt C.AVPacket
			er := C.avcodec_receive_packet(codecCtx, &encPkt)
			if er == C.averror_eagain() || er == C.averror_eof() {
				break
			}
			if er < 0 {
				C.av_frame_free(&out)
				return fmt.Errorf("encode failed: %s", avErr2Str(er))
			}

			C.av_packet_rescale_ts(&encPkt, codecCtx.time_base, outStream.time_base)
			encPkt.stream_index = outStream.index
			if encPkt.duration == 0 {
				encPkt.duration = 1
			}

			if wr := C.av_interleaved_write_frame(outFmt, &encPkt); wr < 0 {
				C.av_packet_unref(&encPkt)
				C.av_frame_free(&out)
				return fmt.Errorf("write frame failed: %s", avErr2Str(wr))
			}
			C.av_packet_unref(&encPkt)
			passPkts++
			logFFmpeg(avLogTrace, "cgo", "gif", fmt.Sprintf("packet written, total=%d", passPkts))
		}
		C.av_frame_free(&out)
	}

	// Flush encoder + trailer
	C.avcodec_send_frame(codecCtx, nil)
	for {
		var encPkt C.AVPacket
		er := C.avcodec_receive_packet(codecCtx, &encPkt)
		if er == C.averror_eagain() || er == C.averror_eof() {
			break
		}
		C.av_packet_rescale_ts(&encPkt, codecCtx.time_base, outStream.time_base)
		encPkt.stream_index = outStream.index
		if encPkt.duration == 0 {
			encPkt.duration = 1
		}
		if wr := C.av_interleaved_write_frame(outFmt, &encPkt); wr < 0 {
			C.av_packet_unref(&encPkt)
			return fmt.Errorf("write frame (flush) failed: %s", avErr2Str(wr))
		}
		C.av_packet_unref(&encPkt)
	}
	if tr := C.av_write_trailer(outFmt); tr < 0 {
		return fmt.Errorf("write trailer failed: %s", avErr2Str(tr))
	}

	logFFmpeg(avLogVerbose, "cgo", "gif", fmt.Sprintf("transcode done: frames=%d, packets=%d", passFrames, passPkts))
	return nil
}
