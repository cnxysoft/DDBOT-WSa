<div align="center">

## DDBOT-WSa

[![Release](https://img.shields.io/github/release/cnxysoft/DDBOT-WSa?style=flat-square&include_prereleases)](https://github.com/cnxysoft/DDBOT-WSa/releases)
[![Downloads](https://img.shields.io/github/downloads/cnxysoft/DDBOT-WSa/total?style=flat-square&color=%239F7AEA&logo=github)](https://github.com/cnxysoft/DDBOT-WSa/releases)
[![Stars](https://img.shields.io/github/stars/cnxysoft/DDBOT-WSa?style=flat-square)](https://github.com/cnxysoft/DDBOT-WSa/stargazers)
[![Go Version](https://img.shields.io/github/go-mod/go-version/cnxysoft/DDBOT-WSa?style=flat-square&logo=go)](https://go.dev/)
[![GoDoc](https://img.shields.io/badge/go-documentation-blue?style=flat-square&logo=go)](https://pkg.go.dev/github.com/cnxysoft/DDBOT-WSa)
[![License](https://img.shields.io/github/license/cnxysoft/DDBOT-WSa?style=flat-square)](./LICENSE)
[![Docs](https://img.shields.io/badge/docs-kizunerwe.github.io-blue?style=flat-square)](https://kizunerwe.github.io/DDBOT-WSa-docs/)

---

</div>

⚠️ 您正在查看 **next** 分支。其他分支：[master](https://github.com/cnxysoft/DDBOT-WSa)（稳定版）| [next-dev](https://github.com/cnxysoft/DDBOT-WSa/tree/next-dev)（开发前沿）-- [详细对比](https://kizunerwe.github.io/DDBOT-WSa-docs/deploy/branches/)

DDBOT-WSa 是基于 DDBOT-ws 的修改版本，通过 WebSocket 连接 OneBot 11 兼容实现端，把 B 站、斗鱼、虎牙、ACFun、YouTube、微博、推特、抖音等平台的直播/动态更新推送到 IM 群。

兼容 LLOneBot / NapCat / Lagrange / Lagrange PMHQ / go-cqhttp 等 OneBot 11 实现端。

> DDBOT **不是聊天机器人**。它只在「订阅对象有更新」和「答复命令」时主动发言，交互被刻意设计成最小程度，正常聊天永远不会误触。

**选择版本：**

- [master](https://github.com/cnxysoft/DDBOT-WSa) — 稳定版
- [next](https://github.com/cnxysoft/DDBOT-WSa/tree/next) — 更多功能（微博 API 模式、ACFUN 动态、推特 API 模式）
- [next-dev](https://github.com/cnxysoft/DDBOT-WSa/tree/next-dev) — 开发前沿（小红书、Twitch、小黑盒、Telegram 推送）

[📊 详细对比 →](https://kizunerwe.github.io/DDBOT-WSa-docs/deploy/branches/)

## 特性

- **多平台订阅推送**：B 站（直播/动态）、斗鱼、虎牙、ACFun、YouTube、微博、TwitCasting、推特、抖音
- **精细推送控制**：按关键字/动态类型过滤、@全体成员或指定人、下播/标题变更提醒、防刷屏去重
- **模板系统**：基于 `text/template` 自定义所有推送/命令/事件格式，支持自定义命令和定时消息
- **权限管理**：命令启用/禁用、单用户命令权限、角色权限
- **插件扩展**：实现 `Concern` 接口即可接入任意订阅源，框架负责轮询、去重、限流、持久化

## 快速开始

```bash
# 1. 下载对应平台的预编译版本
#    https://github.com/cnxysoft/DDBOT-WSa/releases

# 2. 首次运行（自动生成 device.json 和 application.yaml）
./DDBOT

# 3. 让 OneBot 实现端反向连接到 DDBOT
#    ws://127.0.0.1:15630/ws

# 4. 私聊 BOT 发送 /whosyourdaddy 设置管理员

# 5. 群内发送 /watch <UID> 订阅，开播后自动推送
```

首次启动会生成默认配置（模板已启用），并显示 B 站扫码二维码，用 B 站 App 扫描登录即可开始订阅。不配 B 站账号时订阅数建议不超过 5 个。

## 从纯血 DDBOT 迁移

打开 `.lsp.db`，将 `ae` 字段全部替换为 `ex`，重启即可。详见 [迁移指南](https://kizunerwe.github.io/DDBOT-WSa-docs/deploy/connect/migrate/)。

## 注意事项

- BOT 只在群聊内工作，命令可私聊使用以避免刷屏
- 建议密码设置足够强，不建议把 BOT 设为 QQ 群管理员
- BOT 账号可人工登录，注意个人隐私
- 使用 [buntdb](https://github.com/tidwall/buntdb) 作为嵌入式数据库，文件 `.lsp.db`，删除即恢复出厂设置；可用 [buntdb-cli](https://github.com/Sora233/buntdb-cli) 维护，但不要在 BOT 运行时使用

## 文档

📖 **[完整文档](https://kizunerwe.github.io/DDBOT-WSa-docs/)**

| 链接 | 说明 |
|------|------|
| [快速开始](https://kizunerwe.github.io/DDBOT-WSa-docs/quickstart/) | 下载运行 → 连接 OneBot → 完成第一次订阅 |
| [部署与连接](https://kizunerwe.github.io/DDBOT-WSa-docs/deploy/intro/) | 安装、首次配置、对接 LLBot/NapCat/Lagrange，媒体与 FFmpeg，从纯血迁移 |
| [命令手册](https://kizunerwe.github.io/DDBOT-WSa-docs/commands/) | 所有命令速查与详解（watch / config / grant / silence 等） |
| [配置参考](https://kizunerwe.github.io/DDBOT-WSa-docs/config/) | application.yaml 全字段说明（B 站、推特、抖音、代理、模板开关等） |
| [模板系统](https://kizunerwe.github.io/DDBOT-WSa-docs/template/) | 自定义推送/命令/事件格式，全部模板函数，定时消息 |
| [常见问题](https://kizunerwe.github.io/DDBOT-WSa-docs/faq/) | 部署排障、风控、WebSocket 连接等高频问题 |

订阅源详情、插件开发、版本与分支等见文档站完整导航。

## 交流与反馈

- **Issues**：<https://github.com/cnxysoft/DDBOT-WSa/issues>
- **交流群**：980848391（755612788 已满）
- **B 站专栏**：<https://www.bilibili.com/read/cv10602230>

## 致谢

DDBOT-WSa 基于 [Sora233/DDBOT](https://github.com/Sora233/DDBOT) 与 [Hoshinonyaruko/DDBOT-ws](https://github.com/Hoshinonyaruko/DDBOT-ws) 演进而来，感谢所有贡献者。

<div align="center">

![Contributors](https://contrib.rocks/image?repo=cnxysoft/DDBOT-WSa)

</div>

## License

本项目基于 [AGPL-3.0](./LICENSE) 协议开源。使用了 DDBOT 源代码或对其进行修改的项目，须以相同协议开源并标明著作权。
