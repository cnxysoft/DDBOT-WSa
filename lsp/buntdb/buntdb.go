// Package buntdb 为 DDBOT 提供了一个基于 buntdb 的键值存储解决方案。
// 该包支持两种使用方式：
// 1. 全局数据库实例：兼容原有使用方式，通过 InitBuntDB/GetClient/MustGetClient 等函数操作
// 2. 独立数据库实例：通过 NewDB 创建多个独立的数据库实例，实现数据库解耦
package buntdb

import (
	"fmt"
	"github.com/gofrs/flock"
	jsoniter "github.com/json-iterator/go"
	"github.com/modern-go/gls"
	"github.com/tidwall/buntdb"
)

var db *buntdb.DB

const MEMORYDB = ":memory:"
const LSPDB = ".lsp.db"

var json = jsoniter.ConfigCompatibleWithStandardLibrary
var fileLock *flock.Flock

// DB 代表一个 buntdb 数据库实例
// 通过 NewDB 创建的独立数据库实例，可以实现数据库解耦，避免所有模块使用同一个数据库文件
type DB struct {
	*buntdb.DB
	fileLock *flock.Flock
}

// NewDB 创建一个新的数据库实例
// dbpath: 数据库文件路径，使用 ":memory:" 创建内存数据库
// 返回创建的数据库实例和可能的错误
// 示例:
// 
// db, err := buntdb.NewDB(":memory:")
// if err != nil {
//     log.Fatal(err)
// }
// defer db.Close()
// 
// // 使用 db 进行操作
func NewDB(dbpath string) (*DB, error) {
	var buntDB *buntdb.DB
	var fileLock *flock.Flock
	
	if dbpath == "" {
		dbpath = LSPDB
	}
	if dbpath != MEMORYDB {
		var dblock = dbpath + ".lock"
		fileLock = flock.New(dblock)
		ok, err := fileLock.TryLock()
		if err != nil {
			fmt.Printf("buntdb tryLock err: %v", err)
		}
		if !ok {
			return nil, ErrLockNotHold
		}
	}
	buntDB, err := buntdb.Open(dbpath)
	if err != nil {
		if fileLock != nil {
			// 忽略解锁错误
			_ = fileLock.Unlock()
		}
		return nil, err
	}
	if dbpath != MEMORYDB {
		buntDB.SetConfig(buntdb.Config{
			SyncPolicy:           buntdb.EverySecond,
			AutoShrinkPercentage: 10,
			AutoShrinkMinSize:    1 * 1024 * 1024,
		})
	}
	return &DB{DB: buntDB, fileLock: fileLock}, nil
}

// InitBuntDB 初始化buntdb，正常情况下框架会负责初始化
// 该函数用于初始化全局数据库实例，保持向后兼容性
func InitBuntDB(dbpath string) error {
	buntDB, err := NewDB(dbpath)
	if err != nil {
		return err
	}
	db = buntDB.DB
	fileLock = buntDB.fileLock
	return nil
}

// GetClient 获取 buntdb.DB 对象，如果没有初始化会返回 ErrNotInitialized
// 用于向后兼容，获取全局数据库实例
func GetClient() (*buntdb.DB, error) {
	if db == nil {
		return nil, ErrNotInitialized
	}
	return db, nil
}

// MustGetClient 获取 buntdb.DB 对象，如果没有初始化会panic，在编写订阅组件时可以放心调用
// 用于向后兼容，获取全局数据库实例，如果未初始化则会 panic
func MustGetClient() *buntdb.DB {
	if db == nil {
		panic(ErrNotInitialized)
	}
	return db
}

// Close 关闭buntdb，正常情况下框架会负责关闭
// 用于向后兼容，关闭全局数据库实例
func Close() error {
	if db != nil {
		if itx := gls.Get(txKey); itx != nil {
			itx.(*buntdb.Tx).Rollback()
		}
		if err := db.Close(); err != nil {
			return err
		}
		db = nil
	}
	if fileLock != nil {
		return fileLock.Unlock()
	}
	return nil
}

// GetDB 获取当前 DB 实例
// 返回底层的 buntdb.DB 实例
func (d *DB) GetDB() *buntdb.DB {
	return d.DB
}

// Close 关闭数据库实例
// 关闭当前数据库实例并释放相关资源
func (d *DB) Close() error {
	if d.DB != nil {
		if itx := gls.Get(txKey); itx != nil {
			itx.(*buntdb.Tx).Rollback()
		}
		if err := d.DB.Close(); err != nil {
			return err
		}
		d.DB = nil
	}
	if d.fileLock != nil {
		return d.fileLock.Unlock()
	}
	return nil
}