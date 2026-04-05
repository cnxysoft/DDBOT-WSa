# WSClient 连接稳定性修复方案

## 问题总结

### 1. I/O Timeout 导致断连
```
readLoop ReadMessage error: read tcp 127.0.0.1:15630->127.0.0.1:52239: i/o timeout
```
- `PongWait` (60s) 超时导致连接断开
- **根本原因**：心跳间隔 (30s) 和超时时间 (60s) 配置不当
  - 心跳间隔 30 秒，但超时是 60 秒
  - 如果服务端没有响应 Ping，客户端会在 60 秒后超时
  - 但服务端可能根本没收到 Ping，或者 Pong 在传输中丢失

### 2. errChan 缓冲区太小
```go
readErrChan := make(chan error, 1)  // 缓冲区大小为 1
```
- 当错误快速发生时，后续错误被丢弃
- 导致日志中出现多次相同的错误

### 3. 心跳循环没有正确退出
- `heartbeatLoop` 只检查 `stopChan`，不检查连接是否被替换
- 在 `readLoop` 关闭连接后，心跳循环尝试向已关闭的连接写入

## 修复方案

### 1. 调整心跳间隔和超时时间
```go
const (
    PongWait       = 120 * time.Second  // 120s timeout allows ~7 missed heartbeats (15s interval)
    // ...
)

heartbeatInterval: 15 * time.Second, // 15s interval, ~7x longer than PongWait for tolerance
```
**原因**：
- 心跳间隔应该小于超时时间的一半
- 这样即使丢失 1-2 个 Pong，也不会导致超时
- 心跳间隔 15 秒，超时时间 120 秒，可以容忍最多 7 个心跳丢失

### 2. 增大 errChan 缓冲区
```go
readErrChan := make(chan error, 4) // 增大缓冲区，避免错误被丢弃
```

### 3. 在 heartbeatLoop 中添加连接检查
```go
case <-ticker.C:
    // 检查连接是否仍是当前连接
    c.mu.RLock()
    isCurrent := c.conn == conn
    c.mu.RUnlock()
    if !isCurrent {
        wsLogger.Debugf("heartbeatLoop: connection replaced, exiting")
        return
    }
    if err := c.writeRaw(conn, websocket.PingMessage, []byte{}); err != nil {
        wsLogger.Debugf("heartbeatLoop write error: conn=%p, err=%v", conn, err)
        return
    }
```

## 后续优化建议

### 使用 atomic.Value 存储 conn
可以考虑使用 `atomic.Value` 替代 `*websocket.Conn`，将连接存储改为原子操作，消除锁竞争：

```go
type WSClient struct {
    mu        sync.RWMutex
    writeMu   sync.Mutex
    conn      atomic.Value  // 使用 atomic.Value 存储 conn
    // ...
}
```

这需要较大重构，当前修复已解决实际问题，可后续迭代。

## 测试验证

1. 运行长时间测试，观察是否有 I/O Timeout
2. 检查日志中是否有重复的错误
3. 检查心跳是否正常发送
4. 验证重连机制是否正常工作
