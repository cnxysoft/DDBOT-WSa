// Package buntdb 提供了数据库操作的快捷方法
// 包含 ShortCut 结构体和一系列全局函数，支持对 buntdb 数据库进行便捷操作
// 支持两种使用方式：
// 1. 全局函数：Get/Set/GetJson/SetJson 等，操作全局数据库实例
// 2. ShortCut 实例：通过 WithDB 创建绑定到特定数据库实例的 ShortCut，操作独立数据库
package buntdb

import (
	"errors"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/modern-go/gls"
	"github.com/tidwall/buntdb"
	"strconv"
	"strings"
	"time"
)

// extDbSortedSetIndexPrefix sorted set 索引前缀
// 每个 setName 独立索引，索引名为 ss:{setName}，key 模式为 ss:{setName}:*
const extDbSortedSetIndexPrefix = "ss"

// ShortCut 包含了许多数据库的读写helper，只需嵌入即可使用，如果不想嵌入，也可以通过包名调用
// 支持绑定到特定的数据库实例，实现数据库解耦
type ShortCut struct{
	// 如果 db 不为 nil，则使用该实例，否则使用全局实例
	db *DB
}

var shortCut *ShortCut

func init() {
	shortCut = new(ShortCut)
}

var txKey = new(struct{})

var logger = utils.GetModuleLogger("localdb")

// WithDB 创建一个绑定到特定数据库实例的 ShortCut
// 通过该 ShortCut 操作数据库时，会使用指定的数据库实例而非全局实例
// 示例:
// 
// db, _ := buntdb.NewDB(":memory:")
// sc := buntdb.WithDB(db)
// sc.SetFunc("key", "value")
func WithDB(db *DB) *ShortCut {
	return &ShortCut{db: db}
}

// getClient 获取数据库客户端，优先使用实例数据库，其次使用全局数据库
func (s *ShortCut) getClient() (*buntdb.DB, error) {
	// 检查 s 是否为 nil（这种情况可能发生在全局函数调用中）
	if s == nil {
		return GetClient()
	}
	
	if s.db != nil {
		return s.db.GetDB(), nil
	}
	return GetClient()
}

// RWCoverTx 在一个可读可写事务中执行f，注意f的返回值不一定是RWCoverTx的返回值
// 有可能f返回nil，但RWTxCover返回non-nil
// 可以忽略error，但不要简单地用f返回值替代RWTxCover返回值，ref: bilibili/MarkDynamicId
// 需要注意可写事务是唯一的，同一时间只会存在一个可写事务，所有耗时操作禁止放在可写事务中执行
// 在同一Goroutine中，可写事务可以嵌套
func (s *ShortCut) RWCoverTx(f func(tx *buntdb.Tx) error) error {
	if itx := gls.Get(txKey); itx != nil {
		return f(itx.(*buntdb.Tx))
	}
	db, err := s.getClient()
	if err != nil {
		return err
	}
	return db.Update(func(tx *buntdb.Tx) error {
		var err error
		gls.WithEmptyGls(func() {
			gls.Set(txKey, tx)
			err = f(tx)
		})()
		return err
	})
}

// RWCover 在一个可读可写事务中执行f，不同的是它不获取 buntdb.Tx ，而由 f 自己控制。
// 需要注意可写事务是唯一的，同一时间只会存在一个可写事务，所有耗时操作禁止放在可写事务中执行
// 在同一Goroutine中，可写事务可以嵌套
func (s *ShortCut) RWCover(f func() error) error {
	if itx := gls.Get(txKey); itx != nil {
		return f()
	}
	db, err := s.getClient()
	if err != nil {
		return err
	}
	return db.Update(func(tx *buntdb.Tx) error {
		var err error
		gls.WithEmptyGls(func() {
			gls.Set(txKey, tx)
			err = f()
		})()
		return err
	})
}

// RCoverTx 在一个只读事务中执行f。
// 所有写操作会失败或者回滚。
func (s *ShortCut) RCoverTx(f func(tx *buntdb.Tx) error) error {
	if itx := gls.Get(txKey); itx != nil {
		return f(itx.(*buntdb.Tx))
	}
	db, err := s.getClient()
	if err != nil {
		return err
	}
	return db.View(func(tx *buntdb.Tx) error {
		var err error
		gls.WithEmptyGls(func() {
			gls.Set(txKey, tx)
			err = f(tx)
		})()
		return err
	})
}

// RCover 在一个只读事务中执行f，不同的是它不获取 buntdb.Tx ，而由 f 自己控制。
// 所有写操作会失败，或者回滚。
func (s *ShortCut) RCover(f func() error) error {
	if itx := gls.Get(txKey); itx != nil {
		return f()
	}
	db, err := s.getClient()
	if err != nil {
		return err
	}
	return db.View(func(tx *buntdb.Tx) error {
		var err error
		gls.WithEmptyGls(func() {
			gls.Set(txKey, tx)
			err = f()
		})()
		return err
	})
}

// SeqNext 将key上的int64值加上1并保存，返回保存后的值。
// 如果key不存在，则会默认其为0，返回值为1
// 等价于 s.IncInt64(key, 1)
func (s *ShortCut) SeqNext(key string) (int64, error) {
	return s.IncInt64(key, 1)
}

// IncInt64 将key上的int64值加上 value 并保存，返回保存后的值。
// 如果key不存在，则会默认其为0，返回值为1
// 如果key上的value不是一个int64，则会返回错误
func (s *ShortCut) IncInt64(key string, value int64) (int64, error) {
	var result int64
	err := s.RWCover(func() error {
		oldVal, err := s.GetInt64(key, IgnoreNotFoundOpt())
		if err != nil {
			return err
		}
		result = oldVal + value
		return s.SetInt64(key, result)
	})
	if err != nil {
		result = 0
	}
	return result, err
}

// GetJson 获取key对应的value，并通过 json.Unmarshal 到obj上
// 支持 GetIgnoreExpireOpt IgnoreNotFoundOpt GetTTLOpt
func (s *ShortCut) GetJson(key string, obj interface{}, opt ...OptionFunc) error {
	if obj == nil {
		return errors.New("<nil obj>")
	}
	opts := getOption(opt...)
	var value string
	err := s.RCoverTx(func(tx *buntdb.Tx) error {
		var err error
		value, err = s.getWithOpts(tx, key, opts)
		return err
	})
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return nil
	}
	return json.Unmarshal([]byte(value), obj)
}

// SetJson 将obj通过 json.Marshal 转成json字符串，并设置到key上。
// 支持 SetExpireOpt SetKeepLastExpireOpt SetNoOverWriteOpt SetGetIsOverwriteOpt
// SetGetPreviousValueStringOpt SetGetPreviousValueInt64Opt SetGetPreviousValueJsonObjectOpt
func (s *ShortCut) SetJson(key string, obj interface{}, opt ...OptionFunc) error {
	if obj == nil {
		return errors.New("<nil obj>")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	opts := getOption(opt...)
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		return s.setWithOpts(tx, key, string(b), opts)
	})
}

// DeleteInt64 删除key，解析key上的值到int64并返回
// 支持 IgnoreNotFoundOpt
func (s *ShortCut) DeleteInt64(key string, opt ...OptionFunc) (int64, error) {
	return s.int64Wrapper(s.Delete(key, opt...))
}

// GetInt64 通过key获取value，并将value解析成int64
// 支持 GetIgnoreExpireOpt IgnoreNotFoundOpt GetTTLOpt
// 当设置了 IgnoreNotFoundOpt 时，key不存在时会直接返回0，不会返回错误
func (s *ShortCut) GetInt64(key string, opt ...OptionFunc) (int64, error) {
	return s.int64Wrapper(s.Get(key, opt...))
}

// SetInt64 通过key设置int64格式的value
// 支持 SetExpireOpt SetKeepLastExpireOpt SetNoOverWriteOpt SetGetIsOverwriteOpt
// SetGetPreviousValueStringOpt SetGetPreviousValueInt64Opt SetGetPreviousValueJsonObjectOpt
func (s *ShortCut) SetInt64(key string, value int64, opt ...OptionFunc) error {
	return s.Set(key, strconv.FormatInt(value, 10), opt...)
}

// Delete 删除key，并返回key上的值
// 支持 IgnoreNotFoundOpt
func (s *ShortCut) Delete(key string, opt ...OptionFunc) (string, error) {
	opts := getOption(opt...)
	var previous string
	err := s.RWCoverTx(func(tx *buntdb.Tx) error {
		var err error
		previous, err = s.deleteWithOpts(tx, key, opts)
		return err
	})
	return previous, err
}

// Get 通过key获取value
// 支持 GetIgnoreExpireOpt IgnoreNotFoundOpt GetTTLOpt
func (s *ShortCut) Get(key string, opt ...OptionFunc) (string, error) {
	var result string
	opts := getOption(opt...)
	err := s.RCoverTx(func(tx *buntdb.Tx) error {
		var err error
		result, err = s.getWithOpts(tx, key, opts)
		return err
	})
	return result, err
}

// Set 通过key设置value
// 支持 SetExpireOpt SetKeepLastExpireOpt SetNoOverWriteOpt SetGetIsOverwriteOpt
// SetGetPreviousValueStringOpt SetGetPreviousValueInt64Opt SetGetPreviousValueJsonObjectOpt
func (s *ShortCut) Set(key, value string, opt ...OptionFunc) error {
	opts := getOption(opt...)
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		return s.setWithOpts(tx, key, value, opts)
	})
}

// Exist 查询key是否存在，key不存在或者发生任何错误时返回 false
// 支持 GetTTLOpt GetIgnoreExpireOpt
func (s *ShortCut) Exist(key string, opt ...OptionFunc) bool {
	var result bool
	opts := getOption(opt...)
	err := s.RWCoverTx(func(tx *buntdb.Tx) error {
		result = s.existWithOpts(tx, key, opts)
		return nil
	})
	if err != nil {
		if !IsNotFound(err) {
			logger.Errorf("Exist key %v error %v", key, err)
		}
		result = false
	}
	return result
}

// setWithOpts 统一在有option的情况下的set行为，考虑到性能需要手动传 buntdb.Tx
func (s *ShortCut) setWithOpts(tx *buntdb.Tx, key string, value string, opt *option) error {
	var (
		prev     string
		replaced bool
		err      error
		setOpt   *buntdb.SetOptions
	)
	if innerOpt := opt.getInnerExpire(); innerOpt != nil {
		setOpt = innerOpt
	} else if opt.keepLastExpire {
		lastTTL, _ := tx.TTL(key)
		if lastTTL > 0 {
			setOpt = ExpireOption(lastTTL)
		}
	}
	prev, replaced, err = tx.Set(key, value, setOpt)
	if err != nil {
		return err
	}
	opt.setIsOverWrite(replaced)
	opt.setPrevious(prev)
	if replaced && opt.getNoOverWrite() {
		return ErrRollback
	}
	return nil
}

// getWithOpts 统一在有option的情况下的get行为，考虑到性能需要手动传 buntdb.Tx
func (s *ShortCut) getWithOpts(tx *buntdb.Tx, key string, opt *option) (string, error) {
	result, err := tx.Get(key, opt.getIgnoreExpire())
	if opt.getTTL() != nil {
		ttl, _ := tx.TTL(key)
		opt.setTTL(ttl)
	}
	if opt.getIgnoreNotFound() && IsNotFound(err) {
		err = nil
	}
	return result, err
}

// deleteWithOpts 统一在有option的情况下的delete行为，考虑到性能需要手动传 buntdb.Tx
func (s *ShortCut) deleteWithOpts(tx *buntdb.Tx, key string, opt *option) (string, error) {
	result, err := tx.Delete(key)
	if opt.getIgnoreNotFound() && IsNotFound(err) {
		err = nil
	}
	return result, err
}

// existWithOpts 统一在有option的情况下的exist行为，考虑到性能需要手动传 buntdb.Tx
func (s *ShortCut) existWithOpts(tx *buntdb.Tx, key string, opt *option) bool {
	_, err := tx.Get(key, opt.getIgnoreExpire())
	if opt.getTTL() != nil {
		ttl, _ := tx.TTL(key)
		opt.setTTL(ttl)
	}
	return err == nil
}

func (s *ShortCut) int64Wrapper(result string, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return strconv.ParseInt(result, 10, 64)
}

func (s *ShortCut) CreatePatternIndex(patternFunc KeyPatternFunc, suffix []interface{}, less ...func(a, b string) bool) error {
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		var err error
		if len(less) == 0 {
			err = tx.CreateIndex(patternFunc(suffix...), patternFunc(append(suffix[:], "*")...), buntdb.IndexString)
		}
		err = tx.CreateIndex(patternFunc(suffix...), patternFunc(append(suffix[:], "*")...), less...)
		if err == buntdb.ErrIndexExists {
			err = nil
		}
		return err
	})
}

// CreateSortedSetIndex 创建指定 setName 的 sorted set 索引
// 索引名为 ss:{setName}，使用 IndexFloat 支持数值范围查询
// key 格式为 ss:{setName}:{member}，value 为 score 的字符串表示
func (s *ShortCut) CreateSortedSetIndex(setName string) error {
	indexName := extDbSortedSetIndexPrefix + ":" + setName
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		err := tx.CreateIndex(indexName, indexName+":*", buntdb.IndexFloat)
		if err == buntdb.ErrIndexExists {
			err = nil
		}
		return err
	})
}

// RemoveByPrefixAndIndex 遍历每个index，如果一个key满足任意prefix，则删掉
func (s *ShortCut) RemoveByPrefixAndIndex(prefixKey []string, indexKey []string) ([]string, error) {
	var deletedKey []string
	err := s.RWCoverTx(func(tx *buntdb.Tx) error {
		var removeKey = make(map[string]interface{})
		var iterErr error
		for _, index := range indexKey {
			iterErr = tx.Ascend(index, func(key, value string) bool {
				for _, prefix := range prefixKey {
					if strings.HasPrefix(key, prefix) {
						removeKey[key] = struct{}{}
						return true
					}
				}
				return true
			})
			if iterErr != nil {
				return iterErr
			}
		}
		for key := range removeKey {
			_, err := tx.Delete(key)
			if err == nil {
				deletedKey = append(deletedKey, key)
			}
		}
		return nil
	})
	return deletedKey, err
}

// RWCoverTx 在一个可读可写事务中执行f，注意f的返回值不一定是RWCoverTx的返回值
// 有可能f返回nil，但RWTxCover返回non-nil
// 可以忽略error，但不要简单地用f返回值替代RWTxCover返回值，ref: bilibili/MarkDynamicId
// 需要注意可写事务是唯一的，同一时间只会存在一个可写事务，所有耗时操作禁止放在可写事务中执行
// 在同一Goroutine中，可写事务可以嵌套
func RWCoverTx(f func(tx *buntdb.Tx) error) error {
	return shortCut.RWCoverTx(f)
}

// RWCover 在一个可读可写事务中执行f，不同的是它不获取 buntdb.Tx ，而由 f 自己控制。
// 需要注意可写事务是唯一的，同一时间只会存在一个可写事务，所有耗时操作禁止放在可写事务中执行
// 在同一Goroutine中，可写事务可以嵌套
func RWCover(f func() error) error {
	return shortCut.RWCover(f)
}

// RCoverTx 在一个只读事务中执行f。
// 所有写操作会失败或者回滚。
func RCoverTx(f func(tx *buntdb.Tx) error) error {
	return shortCut.RCoverTx(f)
}

// RCover 在一个只读事务中执行f，不同的是它不获取 buntdb.Tx ，而由 f 自己控制。
// 所有写操作会失败，或者回滚。
func RCover(f func() error) error {
	return shortCut.RCover(f)
}

// SeqNext 将key上的int64值加上1并保存，返回保存后的值。
// 如果key不存在，则会默认其为0，返回值为1
// 等价于 IncInt64(key, 1)
func SeqNext(key string) (int64, error) {
	return shortCut.SeqNext(key)
}

// IncInt64 将key上的int64值加上 value 并保存，返回保存后的值。
// 如果key不存在，则会默认其为0，返回值为1
// 如果key上的value不是一个int64，则会返回错误
func IncInt64(key string, value int64) (int64, error) {
	return shortCut.IncInt64(key, value)
}

// GetJson 获取key对应的value，并通过 json.Unmarshal 到obj上
// 支持 GetIgnoreExpireOpt IgnoreNotFoundOpt GetTTLOpt
func GetJson(key string, obj interface{}, opt ...OptionFunc) error {
	return shortCut.GetJson(key, obj, opt...)
}

// SetJson 将obj通过 json.Marshal 转成json字符串，并设置到key上。
// 支持 SetExpireOpt SetKeepLastExpireOpt SetNoOverWriteOpt SetGetIsOverwriteOpt
// SetGetPreviousValueStringOpt SetGetPreviousValueInt64Opt SetGetPreviousValueJsonObjectOpt
func SetJson(key string, obj interface{}, opt ...OptionFunc) error {
	return shortCut.SetJson(key, obj, opt...)
}

// DeleteInt64 删除key，解析key上的值到int64并返回
// 支持 IgnoreNotFoundOpt
func DeleteInt64(key string, opt ...OptionFunc) (int64, error) {
	return shortCut.DeleteInt64(key, opt...)
}

// GetInt64 通过key获取value，并将value解析成int64
// 支持 GetIgnoreExpireOpt IgnoreNotFoundOpt GetTTLOpt
// 当设置了 IgnoreNotFoundOpt 时，key不存在时会直接返回0
func GetInt64(key string, opt ...OptionFunc) (int64, error) {
	return shortCut.GetInt64(key, opt...)
}

// SetInt64 通过key设置int64格式的value
// 支持 SetExpireOpt SetKeepLastExpireOpt SetNoOverWriteOpt SetGetIsOverwriteOpt
// SetGetPreviousValueStringOpt SetGetPreviousValueInt64Opt SetGetPreviousValueJsonObjectOpt
func SetInt64(key string, value int64, opt ...OptionFunc) error {
	return shortCut.SetInt64(key, value, opt...)
}

// Delete 删除key，并返回key上的值
// 支持 IgnoreNotFoundOpt
func Delete(key string, opt ...OptionFunc) (string, error) {
	return shortCut.Delete(key, opt...)
}

// Get 通过key获取value
// 支持 GetIgnoreExpireOpt IgnoreNotFoundOpt GetTTLOpt
func Get(key string, opt ...OptionFunc) (string, error) {
	return shortCut.Get(key, opt...)
}

// Set 通过key设置value
// 支持 SetExpireOpt SetKeepLastExpireOpt SetNoOverWriteOpt SetGetIsOverwriteOpt
// SetGetPreviousValueStringOpt SetGetPreviousValueInt64Opt SetGetPreviousValueJsonObjectOpt
func Set(key, value string, opt ...OptionFunc) error {
	return shortCut.Set(key, value, opt...)
}

// Exist 查询key是否存在，key不存在或者发生任何错误时返回 false
// 支持 GetTTLOpt GetIgnoreExpireOpt
func Exist(key string, opt ...OptionFunc) bool {
	return shortCut.Exist(key, opt...)
}

// ExpireOption 是一个创建 buntdb.SetOptions 的函数糖，当直接操作底层buntdb的时候可以使用。
// 使用本package的时候请使用 SetExpireOpt
func ExpireOption(duration time.Duration) *buntdb.SetOptions {
	if duration <= 0 {
		return nil
	}
	return &buntdb.SetOptions{
		Expires: true,
		TTL:     duration,
	}
}

// RemoveByPrefixAndIndex 遍历每个index，如果一个key满足任意prefix，则删掉
func RemoveByPrefixAndIndex(prefixKey []string, indexKey []string) ([]string, error) {
	return shortCut.RemoveByPrefixAndIndex(prefixKey, indexKey)
}

func CreatePatternIndex(patternFunc KeyPatternFunc, suffix []interface{}, less ...func(a, b string) bool) error {
	return shortCut.CreatePatternIndex(patternFunc, suffix, less...)
}

// ZAdd 添加成员到有序集合
// 索引名为 ss:{setName}，key 格式为 ss:{setName}:{member}，value 为 score 的字符串表示
// 索引会在写入时自动创建（惰性创建）
func (s *ShortCut) ZAdd(setName string, member string, score float64) error {
	indexName := extDbSortedSetIndexPrefix + ":" + setName
	indexKey := indexName + ":" + member
	// 惰性创建索引
	s.CreateSortedSetIndex(setName)
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		_, _, err := tx.Set(indexKey, strconv.FormatFloat(score, 'f', 6, 64), nil)
		return err
	})
}

// ZRangeByScore 根据分数范围获取成员
// 使用 IndexFloat 直接按 score 数值范围查询，无需在 Go 层过滤
// 注意：buntdb 索引在某些情况下可能丢失，会自动重建
func (s *ShortCut) ZRangeByScore(setName string, min, max float64, handler func(member string, score float64) bool) error {
	indexName := extDbSortedSetIndexPrefix + ":" + setName
	// 确保索引存在
	s.ensureSortedSetIndex(setName)
	return s.RCoverTx(func(tx *buntdb.Tx) error {
		return tx.AscendRange(indexName,
			strconv.FormatFloat(min, 'f', 6, 64),
			strconv.FormatFloat(max, 'f', 6, 64),
			func(key, value string) bool {
				// key 格式: ss:{setName}:{member}
				member := key[len(indexName)+1:]
				score, _ := strconv.ParseFloat(value, 64)
				return handler(member, score)
			})
	})
}

// ensureSortedSetIndex 确保索引存在，如果不存在则创建
// 如果索引丢失会重建
func (s *ShortCut) ensureSortedSetIndex(setName string) error {
	indexName := extDbSortedSetIndexPrefix + ":" + setName
	// 先尝试创建索引（如果已存在会忽略错误）
	s.CreateSortedSetIndex(setName)
	// 验证索引是否工作：如果没有数据就不需要验证
	var hasData bool
	s.RCoverTx(func(tx *buntdb.Tx) error {
		tx.Ascend(indexName, func(key, value string) bool {
			hasData = true
			return false // 找到一个就够了，停止遍历
		})
		return nil
	})
	// 如果有数据但索引不工作，需要重建
	if hasData {
		// 检查索引是否能被 AscendRange 使用
		var indexWorks bool
		s.RCoverTx(func(tx *buntdb.Tx) error {
			tx.AscendRange(indexName, "-inf", "+inf", func(key, value string) bool {
				indexWorks = true
				return false
			})
			return nil
		})
		if !indexWorks {
			// 索引丢失，重新创建
			s.rebuildSortedSetIndex(setName)
		}
	}
	return nil
}

// rebuildSortedSetIndex 重建 sorted set 的索引
// 通过遍历所有以 ss:{setName}: 开头的 key 来重建索引
func (s *ShortCut) rebuildSortedSetIndex(setName string) error {
	indexName := extDbSortedSetIndexPrefix + ":" + setName
	// 收集所有需要重建的 key
	var keys []string
	s.RCoverTx(func(tx *buntdb.Tx) error {
		tx.Ascend("", func(key, value string) bool {
			if strings.HasPrefix(key, indexName+":") {
				keys = append(keys, key)
			}
			return true
		})
		return nil
	})
	if len(keys) == 0 {
		return nil
	}
	// 重建索引：通过重新 Set 这些 key
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		for _, key := range keys {
			val, err := tx.Get(key)
			if err != nil {
				continue
			}
			tx.Set(key, val, nil)
		}
		return nil
	})
}

// ZRem 从有序集合移除成员
func (s *ShortCut) ZRem(setName string, members ...string) error {
	indexName := extDbSortedSetIndexPrefix + ":" + setName
	return s.RWCoverTx(func(tx *buntdb.Tx) error {
		for _, member := range members {
			indexKey := indexName + ":" + member
			tx.Delete(indexKey)
		}
		return nil
	})
}

// ZAdd 添加成员到有序集合（全局函数）
func ZAdd(setName string, member string, score float64) error {
	return shortCut.ZAdd(setName, member, score)
}

// ZRangeByScore 根据分数范围获取成员（全局函数）
func ZRangeByScore(setName string, min, max float64, handler func(member string, score float64) bool) error {
	return shortCut.ZRangeByScore(setName, min, max, handler)
}

// ZRem 从有序集合移除成员（全局函数）
func ZRem(setName string, members ...string) error {
	return shortCut.ZRem(setName, members...)
}