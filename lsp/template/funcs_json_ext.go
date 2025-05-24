package template

import (
	"encoding/json"
	"fmt"
	"github.com/tidwall/gjson"
)

func toGJson(input interface{}) gjson.Result {
	switch e := input.(type) {
	case gjson.Result:
		return e
	case string:
		return gjson.Parse(e)
	case []byte:
		return gjson.ParseBytes(e)
	default:
		panic(fmt.Sprintf("invalid input type %T", input))
	}
}

func toJson(input interface{}) []byte {
	marshal, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	return marshal
}
