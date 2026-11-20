package local_pool

import (
	"errors"
	"github.com/Sora233/MiraiGo-Template/utils"
	"github.com/cnxysoft/DDBOT-WSa/image_pool"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"io/ioutil"
	"math/rand"
	"os"
	"sync"
	"time"
)

var logger = utils.GetModuleLogger("local_pool")

type LocalPool struct {
	freshMutex *sync.Mutex
	lifeMu     sync.Mutex
	stop       chan struct{}
	done       chan struct{}
	started    bool
	closed     bool

	imageDir  string
	imageList []string
}

type Image struct {
	Path string
}

func (i *Image) Content() ([]byte, error) {
	return ioutil.ReadFile(i.Path)
}

func (pool *LocalPool) Get(opts ...image_pool.OptionFunc) ([]image_pool.Image, error) {
	if pool == nil {
		return nil, errors.New("pool status down")
	}
	pool.freshMutex.Lock()
	defer pool.freshMutex.Unlock()

	if len(pool.imageList) == 0 {
		return nil, errors.New("no image")
	}

	var (
		result []image_pool.Image
		option = make(image_pool.Option)
		num    = 1
	)

	for _, opt := range opts {
		opt(option)
	}

	for k, v := range option {
		switch k {
		case "num":
			_v, ok := v.(int)
			if ok {
				num = _v
			}
		}
	}

	for i := 0; i < num; i++ {
		result = append(result, &Image{
			Path: pool.imageList[rand.Intn(len(pool.imageList))],
		})
	}

	return result, nil
}

func (pool *LocalPool) RefreshImage() error {
	pool.freshMutex.Lock()
	defer pool.freshMutex.Unlock()
	files, err := localutils.FilePathWalkDir(pool.imageDir)
	if err != nil {
		return err
	} else {
		pool.imageList = files
	}
	return nil
}

func NewLocalPool(path string) (*LocalPool, error) {
	pool, err := PrepareLocalPool(path)
	if err == nil {
		pool.Start()
	}
	return pool, err
}

// PrepareLocalPool validates and loads the directory without starting workers.
func PrepareLocalPool(path string) (*LocalPool, error) {
	if i, err := os.Stat(path); err != nil || i == nil || !i.IsDir() {
		return nil, errors.New("invalid path")
	}
	pool := &LocalPool{
		imageDir:   path,
		freshMutex: new(sync.Mutex),
		imageList:  make([]string, 0),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	err := pool.RefreshImage()
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func (pool *LocalPool) Start() {
	pool.lifeMu.Lock()
	defer pool.lifeMu.Unlock()
	if pool.started || pool.closed {
		return
	}
	pool.started = true
	go func() {
		defer close(pool.done)
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-pool.stop:
				return
			case <-timer.C:
				err := pool.RefreshImage()
				if err != nil {
					logger.Errorf("local pool refresh failed %v", err)
				}
				timer.Reset(time.Minute)
			}
		}
	}()
}

func (pool *LocalPool) Close() error {
	pool.lifeMu.Lock()
	if !pool.closed {
		close(pool.stop)
		pool.closed = true
	}
	started := pool.started
	pool.lifeMu.Unlock()
	if started {
		<-pool.done
	}
	return nil
}
