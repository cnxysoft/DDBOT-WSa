package buntdb_test

import (
	"fmt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"testing"
)

// ExampleDB 展示如何创建和使用独立的数据库实例
func ExampleDB() {
	// 创建一个新的数据库实例
	db, err := buntdb.NewDB(":memory:")
	if err != nil {
		fmt.Printf("创建数据库实例失败: %v\n", err)
		return
	}
	defer db.Close()

	// 创建一个绑定到该数据库实例的ShortCut
	sc := buntdb.WithDB(db)

	// 使用ShortCut操作数据库
	err = sc.Set("key1", "value1")
	if err != nil {
		fmt.Printf("设置键值失败: %v\n", err)
		return
	}

	value, err := sc.Get("key1")
	if err != nil {
		fmt.Printf("获取键值失败: %v\n", err)
		return
	}

	fmt.Printf("获取到的值: %s\n", value)
	// Output: 获取到的值: value1
}

// ExampleGlobalDB 展示如何继续使用原有的全局数据库
func GlobalDB() {
	// 初始化全局数据库
	err := buntdb.InitBuntDB(":memory:")
	if err != nil {
		fmt.Printf("初始化全局数据库失败: %v\n", err)
		return
	}
	defer buntdb.Close()

	// 使用全局函数操作数据库
	err = buntdb.Set("key2", "value2")
	if err != nil {
		fmt.Printf("设置键值失败: %v\n", err)
		return
	}

	value, err := buntdb.Get("key2")
	if err != nil {
		fmt.Printf("获取键值失败: %v\n", err)
		return
	}

	fmt.Printf("获取到的值: %s\n", value)
	// Output: 获取到的值: value2
}

func TestExampleDB(t *testing.T) {
	ExampleDB()
}

func TestExampleGlobalDB(t *testing.T) {
	GlobalDB()
	// 清理可能影响其他测试的全局状态
	buntdb.Close()
}
