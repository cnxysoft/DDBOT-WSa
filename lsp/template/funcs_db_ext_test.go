package template

import (
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestDbExtFuncs(t *testing.T) {
	// 初始化测试数据库
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	
	// 初始化模板数据库
	err := InitTemplateDB(":memory:")
	assert.Nil(t, err)
	defer GetTemplateDB().Close()

	t.Run("TestSetAndGet", func(t *testing.T) {
		// 测试基本的Set和Get功能
		result := Set("test:key1", "value1")
		assert.Equal(t, "", result) // 空字符串表示成功

		value := Get("test:key1")
		assert.Equal(t, "value1", value)

		// 测试不存在的键
		value = Get("nonexistent")
		assert.Contains(t, value, "not found") // 错误信息应该包含"not found"
	})

	t.Run("TestSetJsonAndGetJson", func(t *testing.T) {
		// 测试JSON数据的存储和获取
		data := map[string]interface{}{
			"name": "test",
			"value": 123,
			"active": true,
		}

		result := setJson("test:jsonkey", data)
		assert.Equal(t, "", result) // 空字符串表示成功

		// jsonData := getJson("test:jsonkey")
		// assert.NotNil(t, jsonData)
	})

	t.Run("TestSetInt64AndGetInt64", func(t *testing.T) {
		// 测试int64数据的存储和获取
		result := setInt64("test:intkey", 9876543210)
		assert.Equal(t, "", result) // 空字符串表示成功

		value := getInt64("test:intkey")
		assert.Equal(t, int64(9876543210), value)

		// 测试不存在的键，应该返回0
		value = getInt64("nonexistent")
		assert.Equal(t, int64(0), value)
	})

	t.Run("TestDelData", func(t *testing.T) {
		// 测试数据删除功能
		Set("test:delkey", "todelete")
		assert.Equal(t, "todelete", Get("test:delkey"))

		result := delData("test:delkey")
		assert.Equal(t, "", result) // 空字符串表示成功

		value := Get("test:delkey")
		assert.Contains(t, value, "not found") // 键应该已经不存在了
	})

	t.Run("TestExistData", func(t *testing.T) {
		// 测试键存在性检查
		Set("test:existkey", "exists")
		assert.True(t, existData("test:existkey"))

		// 测试不存在的键
		assert.False(t, existData("nonexistent"))
	})

	t.Run("TestWithOptions", func(t *testing.T) {
		// 测试带选项的操作
		opts := getOptions([]interface{}{"SetExpire"}, "10s")
		result := Set("test:expirekey", "expiring", opts)
		assert.Equal(t, "", result)

		value := Get("test:expirekey")
		assert.Equal(t, "expiring", value)

		// 测试NoOverWrite选项
		opts2 := getOptions([]interface{}{"SetNoOverWrite"})
		result = Set("test:expirekey", "shouldnotoverwrite", opts2)
		assert.Contains(t, result, "key exist") // 应该返回键已存在的错误

		value = Get("test:expirekey")
		assert.Equal(t, "expiring", value) // 值应该没有改变
	})

	t.Run("TestNewDuration", func(t *testing.T) {
		// 测试newDuration函数
		duration := newDuration()
		assert.NotNil(t, duration)
		assert.Equal(t, int64(0), duration.Nanoseconds())
	})
}