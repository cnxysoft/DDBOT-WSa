# HLS 转封装回归样本

`segment.ts` 是一秒的合成 H.264/AAC MPEG-TS 片段，不包含第三方素材。
生成命令：

```sh
ffmpeg -f lavfi -i 'testsrc2=size=64x64:rate=4:duration=1' \
  -f lavfi -i 'sine=frequency=440:sample_rate=44100:duration=1' \
  -c:v libx264 -pix_fmt yuv420p -c:a aac -f mpegts segment.ts
```

测试通过本地 HTTP 服务读取 `index.m3u8`，验证内置 FFmpeg 能将 TS 的编码标记
转换为 MP4 所需的标记。运行测试本身不需要外部 FFmpeg 命令或网络连接。
