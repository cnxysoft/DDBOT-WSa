# buntdb 包使用说明

## 简介

buntdb 包为 DDBOT 提供了一个基于 [buntdb](https://github.com/tidwall/buntdb) 的键值存储解决方案。在本次重构后，该包支持创建多个独立的数据库实例，实现了数据库解耦。

## 主要特性

1. **向后兼容**：保持原有的全局数据库使用方式，不影响现有代码
2. **数据库解耦**：支持创建多个独立的数据库实例
3. **事务支持**：提供读写事务和只读事务支持
4. **便捷操作**：提供丰富的快捷操作函数

## 使用方式

### 1. 全局数据库实例（原有方式）

保持与之前版本的兼容性，使用全局函数操作数据库：

```go
// 初始化全局数据库
err := buntdb.InitBuntDB(":memory:") // 或指定文件路径
if err != nil {
    log.Fatal(err)
}
defer buntdb.Close()

// 使用全局函数操作数据库
err = buntdb.Set("key", "value")
if err != nil {
    log.Fatal(err)
}

value, err := buntdb.Get("key")
if err != nil {
    log.Fatal(err)
}
fmt.Println(value) // 输出: value
```

### 2. 独立数据库实例（新方式）

支持创建多个独立的数据库实例，实现数据库解耦：

```go
// 创建独立的数据库实例
db1, err := buntdb.NewDB(":memory:") // 或指定文件路径
if err != nil {
    log.Fatal(err)
}
defer db1.Close()

db2, err := buntdb.NewDB(":memory:") // 另一个独立的数据库
if err != nil {
    log.Fatal(err)
}
defer db2.Close()

// 创建绑定到特定数据库实例的 ShortCut
sc1 := buntdb.WithDB(db1)
sc2 := buntdb.WithDB(db2)

// 分别操作不同的数据库
err = sc1.SetFunc("key", "value1")
if err != nil {
    log.Fatal(err)
}

err = sc2.SetFunc("key", "value2")
if err != nil {
    log.Fatal(err)
}

value1, _ := sc1.GetFunc("key")
value2, _ := sc2.GetFunc("key")

fmt.Println(value1) // 输出: value1
fmt.Println(value2) // 输出: value2
```

## API 说明

### DB 结构体

- `NewDB(dbpath string) (*DB, error)` - 创建新的数据库实例
- `GetDB() *buntdb.DB` - 获取底层 buntdb.DB 实例
- `Close() error` - 关闭数据库实例

### ShortCut 结构体

- `WithDB(db *DB) *ShortCut` - 创建绑定到特定数据库实例的 ShortCut
- `SetFunc(key, value string, opt ...OptionFunc) error` - 设置键值
- `GetFunc(key string, opt ...OptionFunc) (string, error)` - 获取键值
- `SetJsonFunc(key string, obj interface{}, opt ...OptionFunc) error` - 设置 JSON 对象
- `GetJsonFunc(key string, obj interface{}, opt ...OptionFunc) error` - 获取 JSON 对象
- `DeleteFunc(key string, opt ...OptionFunc) (string, error)` - 删除键
- `ExistFunc(key string, opt ...OptionFunc) bool` - 检查键是否存在
- 以及更多其他便捷方法...

### 全局函数

所有原有的全局函数保持不变，继续操作全局数据库实例：
- `InitBuntDB(dbpath string) error`
- `GetClient() (*buntdb.DB, error)`
- `MustGetClient() *buntdb.DB`
- `Close() error`
- `Set(key, value string, opt ...OptionFunc) error`
- `Get(key string, opt ...OptionFunc) (string, error)`
- 等等...

## 注意事项

1. 当使用 `WithDB` 创建 ShortCut 实例时，所有操作都会针对指定的数据库实例
2. 当直接使用 ShortCut 或全局函数时，操作的是全局数据库实例
3. 每个数据库实例都需要手动调用 `Close()` 方法释放资源
4. 多个数据库实例可以并行使用，实现数据隔离