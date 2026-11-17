package lolicon_pool

import (
	"container/list"
	"context"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/image_pool"
	"sync"
	"time"
)

var logger = utils.GetModuleLogger("lolicon_pool")

type Config struct {
	ApiKey   string
	CacheMin int
	CacheMax int
}

type LoliconPool struct {
	config  *Config
	cache   map[R18Type]*list.List
	cond    *sync.Cond
	changed bool
	lifeMu  sync.Mutex
	stop    chan struct{}
	wg      sync.WaitGroup
	started bool
	closed  bool // protected by cond.L
	ctx     context.Context
	cancel  context.CancelFunc
	fetch   func(context.Context, string, R18Type, string, int) (*Response, error)
}

func KeywordOption(keyword string) image_pool.OptionFunc {
	return func(option image_pool.Option) image_pool.Option {
		option["keyword"] = keyword
		return option
	}
}

func R18Option(r18Type R18Type) image_pool.OptionFunc {
	return func(option image_pool.Option) image_pool.Option {
		option["r18"] = r18Type
		return option
	}
}

func (pool *LoliconPool) Get(options ...image_pool.OptionFunc) ([]image_pool.Image, error) {
	option := make(image_pool.Option)
	for _, optionFunc := range options {
		optionFunc(option)
	}

	var (
		r18     R18Type = R18Off
		keyword string
		num     int = 1
	)
	for k, v := range option {
		switch k {
		case "keyword":
			_v, ok := v.(string)
			if ok {
				keyword = _v
			}
		case "num":
			_v, ok := v.(int)
			if ok {
				num = _v
			}
		case "r18":
			_v, ok := v.(R18Type)
			if ok {
				r18 = _v
			}
		}
	}
	if keyword != "" {
		logger.Debugf("request remote image")
		resp, err := pool.fetch(pool.ctx, pool.config.ApiKey, r18, keyword, num)
		if err != nil {
			return nil, err
		}
		logger.WithField("image num", len(resp.Data)).
			WithField("quota", resp.Quota).
			WithField("quota_min_ttl", resp.QuotaMinTTL).
			Debugf("request done")
		switch resp.Code {
		case 404:
			return nil, ErrNotFound
		case 401:
			return nil, ErrAPIKeyError
		case 429:
			return nil, ErrQuotaExceed
		}
		if resp.Code != 0 {
			return nil, fmt.Errorf("response code %v: %v", resp.Code, resp.Msg)
		}
		var result []image_pool.Image
		for _, img := range resp.Data {
			result = append(result, img)
		}
		return result, nil
	}
	return pool.getCache(r18, num)
}

func (pool *LoliconPool) getCache(r18 R18Type, num int) (result []image_pool.Image, err error) {
	pool.cond.L.Lock()
	defer pool.cond.L.Unlock()

	// 防御未知 R18Type：cache 只初始化了 R18Off/R18On，直接访问会 nil panic
	l, ok := pool.cache[r18]
	if !ok || l == nil {
		err = fmt.Errorf("invalid r18 type %v", r18)
		return
	}

	for i := 0; i < num; i++ {
		if l.Len() == 0 {
			err = pool.fillCacheFromRemote(r18)
			if err != nil {
				logger.WithField("from", "getCache").Errorf("fill cache from remote failed %v", err)
				break
			}
			// 填充成功但仍为空（远端返回 0 张图），继续循环只会反复请求远端
			if l.Len() == 0 {
				err = ErrNotFound
				break
			}
		}
		result = append(result, l.Remove(l.Front()).(*Setu))
		pool.changed = true
	}
	pool.cond.Signal()
	return
}

// caller must hold the lock
func (pool *LoliconPool) fillCacheFromRemote(r18 R18Type) error {
	logger.WithField("r18", r18.String()).Debug("fetch from remote")
	resp, err := pool.fetch(pool.ctx, pool.config.ApiKey, r18, "", 10)
	if err != nil {
		return err
	}
	logger.WithField("Quota", resp.Quota).
		WithField("QuotaMinTTL", resp.QuotaMinTTL).
		WithField("Msg", resp.Msg).
		WithField("Code", resp.Code).
		Debug("LoliconPool response")
	if resp.Code != 0 {
		return fmt.Errorf("response code %v: %v", resp.Code, resp.Msg)
	}
	if l, ok := pool.cache[r18]; ok && l != nil {
		for _, s := range resp.Data {
			l.PushFront(s)
		}
	}
	pool.changed = true
	return nil
}

func (pool *LoliconPool) background() {
	defer pool.wg.Done()
	for {
		var result = true
		pool.cond.L.Lock()
		for {
			if pool.closed {
				pool.cond.L.Unlock()
				return
			}
			var checkResult = false
			for _, v := range pool.cache {
				if v.Len() < pool.config.CacheMin {
					checkResult = true
				}
			}
			if checkResult {
				break
			}
			pool.cond.Wait()
		}
		for r18, l := range pool.cache {
			if l.Len() < pool.config.CacheMin {
				// 限制单轮填充次数，并检测"远端成功但返回 0 张"的情况，
				// 避免缓存始终无法达到 CacheMax 时死循环并永久阻塞 Get
				attempts := 0
				for l.Len() < pool.config.CacheMax && attempts < 3 {
					attempts++
					before := l.Len()
					pool.changed = true
					if err := pool.fillCacheFromRemote(r18); err != nil {
						logger.WithField("from", "background").Errorf("fill cache from remote failed %v", err)
						result = false
						break
					}
					if l.Len() == before {
						result = false
						logger.WithField("from", "background").Warnf("远端返回 0 张图片，停止本轮填充 (r18=%v)", r18)
						break
					}
				}
			}
		}
		pool.cond.L.Unlock()
		if !result {
			select {
			case <-pool.stop:
				return
			case <-time.After(time.Minute):
			}
		}
	}
}

func NewLoliconPool(config *Config) (*LoliconPool, error) {
	pool, err := PrepareLoliconPool(config)
	if err == nil {
		pool.Start()
	}
	return pool, err
}

// PrepareLoliconPool keeps the candidate idle until the transaction commits.
func PrepareLoliconPool(config *Config) (*LoliconPool, error) {
	if config == nil || config.CacheMin < 0 || config.CacheMax < 0 {
		return nil, fmt.Errorf("invalid image cache limits")
	}
	copyConfig := *config
	config = &copyConfig
	// 目前不再需要apikey了
	//if config.ApiKey == "" {
	//	return nil, errors.New("empty api key")
	//}
	if config.CacheMin == 0 {
		config.CacheMin = 20
	}
	if config.CacheMax == 0 {
		config.CacheMax = 50
	}
	if config.CacheMin > config.CacheMax {
		config.CacheMin = config.CacheMax
	}
	pool := &LoliconPool{
		config:  config,
		cache:   make(map[R18Type]*list.List),
		cond:    sync.NewCond(&sync.Mutex{}),
		changed: false,
		stop:    make(chan struct{}),
		fetch:   LoliconAppSetuContext,
	}
	pool.cache[R18Off] = list.New()
	pool.cache[R18On] = list.New()
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	return pool, nil
}

func (pool *LoliconPool) Start() {
	pool.lifeMu.Lock()
	defer pool.lifeMu.Unlock()
	pool.cond.L.Lock()
	closed := pool.closed
	pool.cond.L.Unlock()
	if pool.started || closed {
		return
	}
	pool.started = true
	// The previous generation has flushed and retired before we load its cache.
	pool.load()
	pool.wg.Add(2)
	go pool.background()
	go func() {
		defer pool.wg.Done()
		timer := time.NewTicker(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-pool.stop:
				return
			case <-timer.C:
				pool.store()
			}
		}
	}()
}

func (pool *LoliconPool) Close() error {
	pool.lifeMu.Lock()
	pool.cancel()
	pool.cond.L.Lock()
	if !pool.closed {
		pool.closed = true
		close(pool.stop)
		pool.cond.Broadcast()
	}
	pool.cond.L.Unlock()
	pool.wg.Wait()
	if pool.started {
		pool.store()
		pool.started = false
	}
	pool.lifeMu.Unlock()
	return nil
}
