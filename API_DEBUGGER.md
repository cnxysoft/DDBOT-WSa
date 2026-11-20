# DDBOT-WSa API 调试工具使用指南

## 简介

DDBOT-WSa 现在内置了一个Web版的API调试工具，让你可以直接在浏览器中测试和调试所有的RESTful API接口，无需安装额外的工具。

## 访问方式

启动DDBOT-WSa后，在浏览器中访问：
```
http://localhost:15631/api/debug
```

> 注意：需要确保在配置文件中启用了admin功能：
> ```yaml
> admin:
>   enable: true
>   addr: "127.0.0.1:15631"
>   token: "your-token-here"  # 可选
> ```

## 功能特性

### 🎯 主要功能
- **可视化界面**：现代化的Web界面，操作简单直观
- **实时调试**：直接在浏览器中发送API请求并查看响应
- **智能提示**：内置常用API路径的快速选择按钮
- **自动格式化**：JSON响应自动格式化显示
- **双视图切换**：可分别查看响应头和响应体
- **请求历史**：显示请求耗时和状态码

### 🛠️ 支持的操作
- GET/POST/PUT/DELETE 请求
- 自定义请求头和Authorization Token
- JSON格式请求体编辑
- 响应头和响应体查看
- 错误信息提示

## 使用教程

### 1. 基础使用
1. 打开调试界面
2. 选择或输入API路径（如 `/api/v1/health`）
3. 选择HTTP方法（GET/POST等）
4. 如需要，填写Authorization Token
5. 点击"发送请求"按钮

### 2. 发送POST请求
1. 选择POST方法
2. 输入API路径（如 `/api/v1/subs/add`）
3. 系统会自动显示对应的请求体模板
4. 修改请求体内容
5. 发送请求

### 3. 常用API快速访问
界面提供了以下常用API的快捷按钮：
- **健康检查**：`/api/v1/health`
- **OneBot状态**：`/api/v1/onebot/status`
- **订阅汇总**：`/api/v1/subs/summary`
- **订阅列表**：`/api/v1/subs/list`
- **系统状态**：`/api/v1/status`
- **完整配置**：`/api/v1/config`

## API参考

### 基础信息类
```
GET /api/v1/health          # 健康检查
GET /api/v1/onebot/status   # OneBot连接状态
```

### 订阅管理类
```
GET /api/v1/subs/summary    # 订阅统计
GET /api/v1/subs/list       # 订阅列表
POST /api/v1/subs/add       # 添加订阅
POST /api/v1/subs/remove    # 删除订阅
```

### 配置管理类
```
GET /api/v1/config          # 获取完整配置
POST /api/v1/config         # 更新完整配置
GET /api/v1/config/{key}    # 获取特定配置项
POST /api/v1/config/{key}   # 更新特定配置项
POST /api/v1/config/reload  # 重新加载配置
```

### 日志管理类
```
GET /api/v1/logs            # 获取日志
GET /api/v1/logs/{level}    # 按级别获取日志
POST /api/v1/logs/clear     # 清空日志
```

### 状态管理类
```
GET /api/v1/status          # 系统状态
GET /api/v1/status/concerns # 关注站点状态
GET /api/v1/status/system   # 系统资源状态
```

### 通知管理类
```
GET /api/v1/notifications   # 通知历史
POST /api/v1/notifications/send  # 发送通知
GET /api/v1/notifications/stats  # 通知统计
```

## 请求体示例

### 添加订阅
```json
{
  "site": "bilibili",
  "id": "123456",
  "type": "live",
  "groupCode": 123456789
}
```

### 更新配置项
```json
{
  "value": true
}
```

### 发送通知
```json
{
  "target": "group:123456",
  "message": "测试消息",
  "type": "live"
}
```

## 安全说明

- 如果配置了admin.token，需要在请求中提供Authorization头
- 调试界面本身不需要认证，但发送的API请求需要遵循原有的认证机制
- 建议在生产环境中谨慎使用，避免暴露敏感信息

## 故障排除

### 常见问题

1. **无法访问调试界面**
   - 检查admin是否已启用
   - 确认端口是否正确（默认15631）
   - 检查防火墙设置

2. **API请求返回401/403**
   - 确认是否需要提供Authorization Token
   - 检查Token格式是否正确（Bearer token）

3. **响应内容为空**
   - 检查API路径是否正确
   - 确认请求方法是否匹配
   - 查看服务端日志获取更多信息

## 开发者信息

这个调试工具完全集成在DDBOT-WSa的admin模块中，使用原生HTML/CSS/JavaScript实现，无需额外依赖。代码位于 `admin/server.go` 文件中的 `serveApiDebugger` 函数。