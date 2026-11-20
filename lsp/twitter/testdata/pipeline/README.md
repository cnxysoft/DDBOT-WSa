# 推送流程回归样本

这些文件由 FFmpeg 测试图案生成，不包含第三方图片、音视频或真实推文。
`image.png` 是 64×64 静态图；`source.mp4` 是一秒、四帧的 64×64 H.264 测试图案，
`source.m3u8` 与 `source0.ts` 是它的 HLS 版本。样本不含音轨；H.264/AAC 音视频选流由
`ffmpeg/testdata/hls` 的测试覆盖。

可用系统 FFmpeg 重新生成同类样本：

```sh
ffmpeg -f lavfi -i 'testsrc2=size=64x64:rate=4:duration=1' \
  -c:v libx264 -pix_fmt yuv420p source.mp4
ffmpeg -i source.mp4 -frames:v 1 image.png
ffmpeg -i source.mp4 -c copy -hls_time 1 -hls_list_size 0 source.m3u8
```

测试只访问本地 HTTP 服务，在适配器边界截获消息，不发送到真实群聊。
CGO 后端无需外部 FFmpeg；CLI 后端需要 PATH 中的 `ffmpeg`，无需 `ffprobe`。
