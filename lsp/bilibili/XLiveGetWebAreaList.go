package bilibili

import (
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"strconv"
	"time"
)

const PathWebAreaList = "/xlive/web-interface/v1/index/getWebAreaList?source_id=2"

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
