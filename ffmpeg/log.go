package ffmpeg

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	templUtils "github.com/Sora233/MiraiGo-Template/utils"
	"github.com/sirupsen/logrus"
)

const (
	avLogQuiet   = -8
	avLogPanic   = 0
	avLogFatal   = 8
	avLogError   = 16
	avLogWarning = 24
	avLogInfo    = 32
	avLogVerbose = 40
	avLogDebug   = 48
	avLogTrace   = 56
)

var logger = templUtils.GetModuleLogger("ffmpeg")
var nativeLogLevel atomic.Int32

func init() { EnableFFmpegLog(avLogWarning) }

// SetLogLevel controls FFmpeg's native verbosity. DDBOT's global logLevel still
// filters the records: verbose/debug need debug, and trace needs trace.
// Invalid values leave the current setting unchanged.
func SetLogLevel(value string) error {
	level, ok := parseLogLevel(value)
	if !ok {
		return fmt.Errorf("unknown FFmpeg log level %q", value)
	}
	EnableFFmpegLog(level)
	return nil
}

// EnableFFmpegLog accepts the native AV_LOG_* numeric levels for compatibility.
func EnableFFmpegLog(level int) {
	nativeLogLevel.Store(int32(level))
	configureNativeLogging(level)
}

func parseLogLevel(value string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "quiet", "off", "silent":
		return avLogQuiet, true
	case "panic":
		return avLogPanic, true
	case "fatal":
		return avLogFatal, true
	case "error":
		return avLogError, true
	case "", "warning", "warn":
		return avLogWarning, true
	case "info":
		return avLogInfo, true
	case "verbose":
		return avLogVerbose, true
	case "debug":
		return avLogDebug, true
	case "trace":
		return avLogTrace, true
	default:
		return 0, false
	}
}

func nativeLevelName(level int) string {
	switch {
	case level < avLogPanic:
		return "quiet"
	case level <= avLogPanic:
		return "panic"
	case level <= avLogFatal:
		return "fatal"
	case level <= avLogError:
		return "error"
	case level <= avLogWarning:
		return "warning"
	case level <= avLogInfo:
		return "info"
	case level <= avLogVerbose:
		return "verbose"
	case level <= avLogDebug:
		return "debug"
	default:
		return "trace"
	}
}

func logFFmpeg(level int, backend, component, message string) {
	if level < avLogPanic || level > int(nativeLogLevel.Load()) {
		return
	}
	ddLevel := logrus.ErrorLevel
	switch {
	case level > avLogDebug:
		ddLevel = logrus.TraceLevel
	case level > avLogInfo:
		ddLevel = logrus.DebugLevel
	case level > avLogWarning:
		ddLevel = logrus.InfoLevel
	case level > avLogError:
		ddLevel = logrus.WarnLevel
	}
	if !logger.Logger.IsLevelEnabled(ddLevel) {
		return
	}
	// A native PANIC/FATAL is a media error, not a request to panic or exit DDBOT.
	entry := logger.WithFields(logrus.Fields{"backend": backend, "ffmpeg_level": nativeLevelName(level)})
	if component != "" {
		entry = entry.WithField("component", component)
	}
	for _, line := range strings.Split(message, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			entry.Log(ddLevel, line)
		}
	}
}

var cliLevelPattern = regexp.MustCompile(`\[(panic|fatal|error|warning|info|verbose|debug|trace)\]`)

// exec.Cmd drains stderr through this writer before Run returns. Bound unfinished
// lines and flush the final fragment so early CLI failures reach DD's logger.
type cliLogWriter struct {
	mu        sync.Mutex
	pending   []byte
	truncated bool
}

func (w *cliLogWriter) emit(line string) {
	level, component := avLogError, ""
	if match := cliLevelPattern.FindStringSubmatchIndex(line); match != nil {
		level, _ = parseLogLevel(line[match[2]:match[3]])
		prefix := strings.TrimSpace(line[:match[0]])
		if strings.HasPrefix(prefix, "[") && strings.HasSuffix(prefix, "]") {
			component = strings.SplitN(strings.Trim(prefix, "[]"), " @ ", 2)[0]
		}
		line = line[match[1]:]
	}
	logFFmpeg(level, "cli", component, line)
}

func (w *cliLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, part := range bytes.SplitAfter(p, []byte{'\n'}) {
		n := min(len(part), 16*1024-len(w.pending))
		w.pending = append(w.pending, part[:n]...)
		w.truncated = w.truncated || n < len(part)
		if len(part) > 0 && part[len(part)-1] == '\n' {
			w.flushLine()
		}
	}
	return len(p), nil
}

func (w *cliLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.flushLine()
	}
}

func (w *cliLogWriter) flushLine() {
	line := string(w.pending)
	if w.truncated {
		line += " [truncated]"
	}
	w.emit(line)
	w.pending, w.truncated = w.pending[:0], false
}
