package ffmpeg

import (
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

func captureFFmpegLogs(t *testing.T) (*logrus.Logger, *logtest.Hook) {
	t.Helper()
	oldLogger, oldLevel := logger, int(nativeLogLevel.Load())
	log := logrus.New()
	log.SetOutput(io.Discard)
	log.SetLevel(logrus.TraceLevel)
	hook := logtest.NewLocal(log)
	logger = log.WithField("module", "ffmpeg")
	t.Cleanup(func() { logger = oldLogger; EnableFFmpegLog(oldLevel) })
	return log, hook
}

func TestNativeLogLevelsUseDDFormatAndSeverity(t *testing.T) {
	_, hook := captureFFmpegLogs(t)
	require.NoError(t, SetLogLevel("trace"))
	for _, tc := range []struct {
		native int
		dd     logrus.Level
	}{
		{avLogPanic, logrus.ErrorLevel}, {avLogFatal, logrus.ErrorLevel},
		{avLogError, logrus.ErrorLevel}, {avLogWarning, logrus.WarnLevel},
		{avLogInfo, logrus.InfoLevel}, {avLogVerbose, logrus.DebugLevel},
		{avLogDebug, logrus.DebugLevel}, {avLogTrace, logrus.TraceLevel},
	} {
		hook.Reset()
		logFFmpeg(tc.native, "cgo", "hls", "  native message\n\n")
		require.Len(t, hook.AllEntries(), 1)
		entry := hook.LastEntry()
		require.Equal(t, tc.dd, entry.Level)
		require.Equal(t, "native message", entry.Message)
		require.Equal(t, logrus.Fields{"module": "ffmpeg", "backend": "cgo", "component": "hls", "ffmpeg_level": nativeLevelName(tc.native)}, entry.Data)
	}
}

func TestLogThresholdAndInvalidConfiguration(t *testing.T) {
	log, hook := captureFFmpegLogs(t)
	require.NoError(t, SetLogLevel(" WARN "))
	logFFmpeg(avLogInfo, "cgo", "", "hidden info")
	logFFmpeg(avLogWarning, "cgo", "", "visible warning")
	require.Len(t, hook.AllEntries(), 1)
	require.Error(t, SetLogLevel("verbose-typo"))
	require.EqualValues(t, avLogWarning, nativeLogLevel.Load())
	require.NoError(t, SetLogLevel("quiet"))
	logFFmpeg(avLogError, "cgo", "", "hidden error")
	require.Len(t, hook.AllEntries(), 1)
	require.NoError(t, SetLogLevel("trace"))
	log.SetLevel(logrus.InfoLevel)
	logFFmpeg(avLogDebug, "cgo", "", "filtered by DD")
	require.Len(t, hook.AllEntries(), 1)
}

func TestCLIStderrIsStructuredAcrossWrites(t *testing.T) {
	_, hook := captureFFmpegLogs(t)
	require.NoError(t, SetLogLevel("trace"))
	w := &cliLogWriter{}
	_, err := w.Write([]byte("[hls @ 0x123] [warn"))
	require.NoError(t, err)
	_, err = w.Write([]byte("ing] slow input\n[error] read failed\n[debug] details\n[fatal] trailing fragment"))
	require.NoError(t, err)
	w.Flush()
	entries := hook.AllEntries()
	require.Len(t, entries, 4)
	for i, level := range []logrus.Level{logrus.WarnLevel, logrus.ErrorLevel, logrus.DebugLevel, logrus.ErrorLevel} {
		require.Equal(t, level, entries[i].Level)
		require.Equal(t, "cli", entries[i].Data["backend"])
		require.Equal(t, "ffmpeg", entries[i].Data["module"])
	}
	require.Equal(t, "hls", entries[0].Data["component"])
	require.Equal(t, "slow input", entries[0].Message)
	require.Equal(t, "trailing fragment", entries[3].Message)
	hook.Reset()
	_, err = w.Write([]byte("[debug] " + strings.Repeat("x", 40*1024)))
	require.NoError(t, err)
	require.LessOrEqual(t, len(w.pending), 16*1024)
	w.Flush()
	require.Len(t, hook.AllEntries(), 1)
	require.Equal(t, logrus.DebugLevel, hook.LastEntry().Level)
	require.Contains(t, hook.LastEntry().Message, "[truncated]")
}
