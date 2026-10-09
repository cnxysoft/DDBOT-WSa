# DDBOT-Lite（证据驱动重实现）

> 本分支是 [阿宇](https://github.com/why0209) 的私有分支 **DDBOT-Lite** 的**证据驱动重实现**，
> 作为一个**只读参考分支**存在，供逐步移植进 `next-dev`。
>
> **不是源码复制**。原作者因工作原因已放弃维护并自行删除源代码，本分支的全部代码是在没有源码的前提下、
> 以运行成品二进制为唯一依据重新实现的。已获原作者授权，许可证沿用 **AGPL-3.0**。详见 [NOTICE.md](NOTICE.md)。

## 启用平台

B 站（直播 / 动态）、微博、抖音、猫耳FM。

源码树中仍保留上游其它平台的包，但本分支未注册它们（与成品的符号集合一致）。

## 构建

需要 Go 1.27.1，入口是 `./cmd`：

```bash
go build -o DDBOT ./cmd
```

仓库含 5 个模块（根、`adapter`、`bot`、`lsp/eventbus`、`utils/qqlog`），以相对路径 `replace` 关联，
请完整 clone，不要只取子目录；`adapter` 是独立模块，需单独 `cd adapter && go test ./...`。

## 一致性

- **符号覆盖**：关闭内联构建（`-gcflags=all=-l`）702/702 = 100%（真缺口 0）；默认构建 659/702 = 93.9%（差额为 go1.27 内联产物）。
- `go build ./cmd` / `go vet ./...` 通过；30 个包单测通过（`TestCheckUpdate` 依赖 GitHub API，偶发失败）。
- **未做端到端生产验证**。已知差异与未坐实项见 [NOTICE.md](NOTICE.md)。

## 文档

部署、对接 OneBot、命令、配置、模板与排障见[主线文档站](https://kizunerwe.github.io/DDBOT-WSa-docs/)。

## License

[AGPL-3.0](./LICENSE)。
