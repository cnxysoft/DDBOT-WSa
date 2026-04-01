package bilibili

import (
	jsoniter "github.com/json-iterator/go"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

const PathWebAreaList = "/xlive/web-interface/v1/index/getWebAreaList?source_id=2"

const AreaListFilePath = "./res/bilibili_area_list.json"

// areaListFileData 用于序列化存储的结构
type areaListFileData struct {
	Expid int32       `json:"expid"`
	Data  []*Category `json:"data"`
}

func getAreaListFilePath() string {
	absPath, _ := filepath.Abs(AreaListFilePath)
	return absPath
}

// LoadAreaListFromFile 从文件加载AreaData
func LoadAreaListFromFile() *AreaData {
	filePath := getAreaListFilePath()
	data, err := os.ReadFile(filePath)
	if err != nil {
		logger.Tracef("bilibili: loadAreaListFromFile error %v", err)
		return nil
	}
	var fileData areaListFileData
	if err := jsoniter.Unmarshal(data, &fileData); err != nil {
		logger.Errorf("bilibili: loadAreaListFromFile unmarshal error %v", err)
		return nil
	}
	logger.Tracef("bilibili: loadAreaListFromFile ok")
	return &AreaData{
		Expid: fileData.Expid,
		Data:  fileData.Data,
	}
}

// SaveAreaListToFile 保存AreaData到文件
func SaveAreaListToFile(areaData *AreaData) error {
	if areaData == nil {
		return nil
	}
	filePath := getAreaListFilePath()
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Errorf("bilibili: saveAreaListToFile mkdir error %v", err)
		return err
	}
	fileData := areaListFileData{
		Expid: areaData.GetExpid(),
		Data:  areaData.GetData(),
	}
	data, err := jsoniter.Marshal(fileData)
	if err != nil {
		logger.Errorf("bilibili: saveAreaListToFile marshal error %v", err)
		return err
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		logger.Errorf("bilibili: saveAreaListToFile write error %v", err)
		return err
	}
	logger.Tracef("bilibili: saveAreaListToFile ok")
	return nil
}

// LoadOrRefreshAreaList 被动加载或刷新分区数据
// 如果本地文件存在且可用则直接加载，否则拉取并保存
func LoadOrRefreshAreaList() *AreaData {
	// 先尝试从文件加载
	areaData := LoadAreaListFromFile()
	if areaData != nil && len(areaData.GetData()) > 0 {
		return areaData
	}
	// 文件不存在或无效，拉取新数据
	areaData = RefreshAreaList()
	if areaData != nil {
		SaveAreaListToFile(areaData)
	}
	return areaData
}

// RefreshAreaListAndSave 拉取分区数据并保存到文件
func RefreshAreaListAndSave() *AreaData {
	areaData := RefreshAreaList()
	if areaData != nil {
		SaveAreaListToFile(areaData)
	}
	return areaData
}

func RefreshAreaList() *AreaData {
	path := BPath(PathWebAreaList)
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second * 15),
		AddUAOption(),
		delete412ProxyOption,
	}
	var resp = new(XLiveGetWebAreaListResponse)
	err := requests.Get(path, nil, resp, opts...)
	if err != nil {
		logger.Errorf("bilibili: refreshAreaList error %v", err)
		return nil
	}
	areaData := resp.GetData()
	if areaData != nil {
		return areaData
	}
	logger.Trace("bilibili: refreshAreaList ok")
	return nil
}

func (a *AreaData) GetSubArea(id int32) *Category {
	for _, v := range a.GetData() {
		if v.GetId() == id {
			return v
		}
	}
	return nil
}

func (c *Category) GetSubCategory(id int32) *Item {
	for _, v := range c.GetList() {
		subId, err := strconv.Atoi(v.GetId())
		if err != nil {
			logger.Errorf("bilibili: GetSubCategory error %v", err)
			continue
		}
		if int32(subId) == id {
			return v
		}
	}
	return nil
}
